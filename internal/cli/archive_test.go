package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tammersaleh/confluence-cli/internal/output"
)

// archiveWorld is a fake Confluence for the archive tests. Every map is keyed
// by page id.
type archiveWorld struct {
	mu sync.Mutex

	// statuses is the sequence of statuses successive GETs of a page report;
	// the last entry repeats. A page absent from the map is "current".
	statuses map[string][]string
	missing  map[string]bool // page GET 404s
	folders  map[string]bool // folder GET succeeds
	children map[string]bool // direct-children returns one child

	postStatus map[string]int      // non-zero: archive POST answers this status
	tasks      map[string][]string // long-task bodies per page; the last repeats

	gqlFail map[string]bool // unarchive mutation answers a GraphQL error

	archived   []string // ids the archive POST accepted
	unarchived []string // ids the unarchive mutation accepted
	gets       map[string]int
	polls      map[string]int
}

const (
	taskRunning  = `{"id":"T","successful":false,"finished":false,"messages":[{"translation":""}],"status":"RUNNING","errors":[]}`
	taskFinished = `{"id":"T","successful":true,"finished":true,"messages":[{"translation":"PageArchivedList :: [1]"}],"status":"FINISH_SUCCESS","errors":[]}`
	taskGone     = "404" // the long-task GET answers 404
	taskFailed   = `{"id":"T","successful":false,"finished":true,"messages":[],"status":"FINISH_FAILED","errors":[{"message":"boom"}]}`
)

func (w *archiveWorld) server(t *testing.T) *httptest.Server {
	t.Helper()
	w.gets = map[string]int{}
	w.polls = map[string]int{}
	return httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.mu.Lock()
		defer w.mu.Unlock()
		p := r.URL.Path
		switch {
		case p == "/wiki/rest/api/content/archive" && r.Method == http.MethodPost:
			var body struct {
				Pages []struct {
					ID string `json:"id"`
				} `json:"pages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if len(body.Pages) != 1 {
				t.Errorf("archive POST carried %d pages, want 1", len(body.Pages))
				rw.WriteHeader(http.StatusBadRequest)
				return
			}
			id := body.Pages[0].ID
			if code := w.postStatus[id]; code != 0 {
				rw.WriteHeader(code)
				_, _ = rw.Write([]byte(`{"statusCode":400,"data":{"errors":[{"message":{"key":"Cannot archive pages because some or all pages in the input are already archived."}}]},"message":"BadRequestException: Incorrect arguments."}`))
				return
			}
			w.archived = append(w.archived, id)
			rw.WriteHeader(http.StatusAccepted)
			_, _ = rw.Write([]byte(`{"id":"t-` + id + `","links":{"status":"/rest/api/longtask/t-` + id + `"}}`))
		case p == "/gateway/api/graphql" && r.Method == http.MethodPost:
			var body struct {
				Variables struct {
					IDs []json.Number `json:"ids"`
				} `json:"variables"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if len(body.Variables.IDs) != 1 {
				t.Errorf("unarchive mutation carried %d ids, want 1", len(body.Variables.IDs))
				rw.WriteHeader(http.StatusBadRequest)
				return
			}
			id := body.Variables.IDs[0].String()
			if w.gqlFail[id] {
				_, _ = rw.Write([]byte(`{"errors":[{"message":"ServiceException: boom"}],"data":{"bulkUnarchivePages":null}}`))
				return
			}
			w.unarchived = append(w.unarchived, id)
			_, _ = rw.Write([]byte(`{"data":{"bulkUnarchivePages":{"status":true,"taskId":"t-` + id + `"}}}`))
		case strings.HasPrefix(p, "/wiki/rest/api/longtask/t-"):
			id := strings.TrimPrefix(p, "/wiki/rest/api/longtask/t-")
			seq := w.tasks[id]
			if len(seq) == 0 {
				seq = []string{taskFinished}
			}
			n := w.polls[id]
			w.polls[id]++
			if n >= len(seq) {
				n = len(seq) - 1
			}
			if seq[n] == taskGone {
				rw.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = rw.Write([]byte(seq[n]))
		case strings.HasPrefix(p, "/wiki/api/v2/folders/"):
			if !w.folders[strings.TrimPrefix(p, "/wiki/api/v2/folders/")] {
				rw.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = rw.Write([]byte(folderFixture))
		case strings.HasSuffix(p, "/direct-children"):
			id := strings.TrimSuffix(strings.TrimPrefix(p, "/wiki/api/v2/pages/"), "/direct-children")
			if w.children[id] {
				_, _ = rw.Write([]byte(`{"results":[{"id":"900","title":"Child","type":"page"}],"_links":{}}`))
				return
			}
			_, _ = rw.Write([]byte(`{"results":[],"_links":{}}`))
		case strings.HasPrefix(p, "/wiki/api/v2/pages/") && r.Method == http.MethodGet:
			id := strings.TrimPrefix(p, "/wiki/api/v2/pages/")
			if w.missing[id] || w.folders[id] {
				rw.WriteHeader(http.StatusNotFound)
				return
			}
			status := "current"
			if seq := w.statuses[id]; len(seq) > 0 {
				n := w.gets[id]
				if n >= len(seq) {
					n = len(seq) - 1
				}
				status = seq[n]
			}
			w.gets[id]++
			_, _ = rw.Write([]byte(`{"id":"` + id + `","title":"Page ` + id + `","spaceId":"s1","status":"` + status + `","authorId":"u1","createdAt":"2024-01-01T00:00:00Z","body":{"storage":{"value":"<p>x</p>"}},"version":{"number":3,"createdAt":"2024-06-20T00:00:00Z"},"_links":{"webui":"/spaces/T/pages/` + id + `/P"}}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, p)
			rw.WriteHeader(http.StatusNotFound)
		}
	}))
}

// runArchive runs cmd against w and returns the parsed stdout lines and the
// command's error.
func runArchive(t *testing.T, w *archiveWorld, run func(c *CLI) error) ([]map[string]any, error) {
	t.Helper()
	clearCredEnv(t)
	server := w.server(t)
	t.Cleanup(server.Close)
	setEnvCreds(t, server.URL)

	old := longTaskPollInterval
	longTaskPollInterval = time.Millisecond
	t.Cleanup(func() { longTaskPollInterval = old })

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "")
	err := run(c)
	return parseLines(t, out.String()), err
}

func metaErrorCount(t *testing.T, lines []map[string]any) any {
	t.Helper()
	meta, ok := lines[len(lines)-1]["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("last line is not a _meta trailer: %v", lines[len(lines)-1])
	}
	return meta["error_count"]
}

func TestPageArchive_ArchivesAndPolls(t *testing.T) {
	w := &archiveWorld{tasks: map[string][]string{"1": {taskRunning, taskRunning, taskFinished}}}
	lines, err := runArchive(t, w, func(c *CLI) error {
		return (&PageArchiveCmd{Refs: []string{"1", "2"}}).Run(c)
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if strings.Join(w.archived, ",") != "1,2" {
		t.Errorf("archived = %v, want one POST per page for 1 and 2", w.archived)
	}
	if w.polls["1"] != 3 {
		t.Errorf("polls for page 1 = %d, want 3 (running, running, finished)", w.polls["1"])
	}
	row := lines[0]
	if row["input"] != "1" || row["id"] != "1" || row["title"] != "Page 1" || row["archived"] != true || row["status"] != "archived" {
		t.Errorf("row unexpected: %v", row)
	}
	if lines[1]["id"] != "2" || lines[1]["archived"] != true {
		t.Errorf("second row unexpected: %v", lines[1])
	}
}

func TestPageArchive_DryRun(t *testing.T) {
	w := &archiveWorld{}
	lines, err := runArchive(t, w, func(c *CLI) error {
		return (&PageArchiveCmd{Refs: []string{"1"}, DryRun: true}).Run(c)
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(w.archived) != 0 {
		t.Errorf("dry-run must not POST, got %v", w.archived)
	}
	if row := lines[0]; row["would_archive"] != true || row["id"] != "1" || row["title"] != "Page 1" {
		t.Errorf("dry-run row unexpected: %v", row)
	}
}

func TestPageArchive_AlreadyArchivedIsNoOp(t *testing.T) {
	w := &archiveWorld{statuses: map[string][]string{"1": {"archived"}}}
	lines, err := runArchive(t, w, func(c *CLI) error {
		return (&PageArchiveCmd{Refs: []string{"1"}}).Run(c)
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(w.archived) != 0 {
		t.Errorf("already-archived page must not POST, got %v", w.archived)
	}
	if row := lines[0]; row["archived"] != false || row["status"] != "archived" {
		t.Errorf("no-op row unexpected: %v", row)
	}
}

func TestPageArchive_Refusals(t *testing.T) {
	cases := []struct {
		name    string
		world   *archiveWorld
		wantErr string
	}{
		{"trashed page", &archiveWorld{statuses: map[string][]string{"1": {"trashed"}}}, "page_trashed"},
		{"folder id", &archiveWorld{folders: map[string]bool{"1": true}}, "not_a_page"},
		{"missing page", &archiveWorld{missing: map[string]bool{"1": true}}, "page_not_found"},
		{"page with children", &archiveWorld{children: map[string]bool{"1": true}}, "has_children"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, dryRun := range []bool{false, true} {
				lines, err := runArchive(t, tc.world, func(c *CLI) error {
					return (&PageArchiveCmd{Refs: []string{"1"}, DryRun: dryRun}).Run(c)
				})
				if !errors.As(err, new(*output.ExitError)) {
					t.Fatalf("dryRun=%v: expected *output.ExitError, got %T: %v", dryRun, err, err)
				}
				if len(tc.world.archived) != 0 {
					t.Errorf("dryRun=%v: must not POST, got %v", dryRun, tc.world.archived)
				}
				if lines[0]["error"] != tc.wantErr || lines[0]["input"] != "1" {
					t.Errorf("dryRun=%v: row = %v, want inline %s", dryRun, lines[0], tc.wantErr)
				}
				if got := metaErrorCount(t, lines); got != float64(1) {
					t.Errorf("dryRun=%v: error_count = %v, want 1", dryRun, got)
				}
			}
		})
	}
}

func TestPageArchive_TaskFailure(t *testing.T) {
	w := &archiveWorld{tasks: map[string][]string{"1": {taskFailed}}}
	lines, err := runArchive(t, w, func(c *CLI) error {
		return (&PageArchiveCmd{Refs: []string{"1", "2"}}).Run(c)
	})
	if !errors.As(err, new(*output.ExitError)) {
		t.Fatalf("expected *output.ExitError, got %T: %v", err, err)
	}
	row := lines[0]
	if row["error"] != "archive_failed" || row["input"] != "1" {
		t.Fatalf("row = %v, want inline archive_failed", row)
	}
	data, _ := row["data"].(map[string]any)
	if data["task_id"] != "t-1" {
		t.Errorf("data.task_id = %v, want t-1", data["task_id"])
	}
	if errs, _ := data["errors"].([]any); len(errs) != 1 {
		t.Errorf("data.errors = %v, want the task's one error", data["errors"])
	}
	if lines[1]["archived"] != true {
		t.Errorf("a failed task must not stop later pages: %v", lines[1])
	}
}

// A finished task that claims success but carries errors fails closed.
func TestPageArchive_SuccessWithErrorsFailsClosed(t *testing.T) {
	w := &archiveWorld{tasks: map[string][]string{"1": {`{"id":"T","successful":true,"finished":true,"status":"FINISH_SUCCESS","errors":[{"message":"partial"}]}`}}}
	lines, _ := runArchive(t, w, func(c *CLI) error {
		return (&PageArchiveCmd{Refs: []string{"1"}}).Run(c)
	})
	if lines[0]["error"] != "archive_failed" {
		t.Errorf("row = %v, want archive_failed", lines[0])
	}
}

func TestPageArchive_TimeoutAfterSubmitIsPending(t *testing.T) {
	w := &archiveWorld{tasks: map[string][]string{"1": {taskRunning}}}
	lines, err := runArchive(t, w, func(c *CLI) error {
		c.Timeout = 100 * time.Millisecond
		return (&PageArchiveCmd{Refs: []string{"1", "2"}}).Run(c)
	})
	var exitErr *output.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *output.ExitError, got %T: %v", err, err)
	}
	if exitErr.Code != output.ExitNetwork {
		t.Errorf("exit code = %d, want %d (timeout)", exitErr.Code, output.ExitNetwork)
	}
	row := lines[0]
	if row["error"] != "archive_pending" || row["input"] != "1" {
		t.Fatalf("row = %v, want inline archive_pending", row)
	}
	data, _ := row["data"].(map[string]any)
	if data["id"] != "1" || data["task_id"] != "t-1" {
		t.Errorf("data = %v, want id 1 and task_id t-1", data)
	}
	if len(lines) != 2 {
		t.Errorf("got %d lines, want the pending row and the trailer only: %v", len(lines), lines)
	}
	if strings.Join(w.archived, ",") != "1" {
		t.Errorf("archived = %v, want page 2 never submitted", w.archived)
	}
}

// The POST is not idempotent: a retried request whose first attempt committed
// comes back 400 "already archived". The read-back turns that into success.
func TestPageArchive_SubmitErrorReconciledByReadBack(t *testing.T) {
	w := &archiveWorld{
		statuses:   map[string][]string{"1": {"current", "archived"}},
		postStatus: map[string]int{"1": http.StatusBadRequest},
	}
	lines, err := runArchive(t, w, func(c *CLI) error {
		return (&PageArchiveCmd{Refs: []string{"1"}}).Run(c)
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if row := lines[0]; row["archived"] != true || row["status"] != "archived" {
		t.Errorf("row = %v, want a success row", row)
	}
}

func TestPageArchive_SubmitErrorStaysAnError(t *testing.T) {
	w := &archiveWorld{postStatus: map[string]int{"1": http.StatusBadRequest}}
	lines, err := runArchive(t, w, func(c *CLI) error {
		return (&PageArchiveCmd{Refs: []string{"1"}}).Run(c)
	})
	if !errors.As(err, new(*output.ExitError)) {
		t.Fatalf("expected *output.ExitError, got %T: %v", err, err)
	}
	row := lines[0]
	if row["error"] != "api_error" || !strings.Contains(row["detail"].(string), "already archived") {
		t.Errorf("row = %v, want api_error carrying the server's reason", row)
	}
}

func TestPageArchive_MixedSiteRejected(t *testing.T) {
	clearCredEnv(t)
	setEnvCreds(t, "https://acme.atlassian.net")

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "")
	err := (&PageArchiveCmd{Refs: []string{
		"https://acme.atlassian.net/wiki/spaces/ENG/pages/1/A",
		"https://other.atlassian.net/wiki/spaces/ENG/pages/2/B",
	}}).Run(c)
	var oErr *output.Error
	if !errors.As(err, &oErr) || oErr.Err != "invalid_input" {
		t.Fatalf("error = %v, want invalid_input", err)
	}
}

func TestPageUnarchive_UnarchivesAndPolls(t *testing.T) {
	w := &archiveWorld{
		statuses: map[string][]string{"1": {"archived"}},
		tasks:    map[string][]string{"1": {taskRunning, taskFinished}},
	}
	lines, err := runArchive(t, w, func(c *CLI) error {
		return (&PageUnarchiveCmd{Refs: []string{"1"}}).Run(c)
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if strings.Join(w.unarchived, ",") != "1" {
		t.Errorf("unarchived = %v, want one mutation for page 1", w.unarchived)
	}
	if w.polls["1"] != 2 {
		t.Errorf("polls = %d, want 2", w.polls["1"])
	}
	row := lines[0]
	if row["input"] != "1" || row["id"] != "1" || row["title"] != "Page 1" || row["unarchived"] != true || row["status"] != "current" {
		t.Errorf("row unexpected: %v", row)
	}
}

func TestPageUnarchive_NotArchivedIsNoOp(t *testing.T) {
	w := &archiveWorld{}
	lines, err := runArchive(t, w, func(c *CLI) error {
		return (&PageUnarchiveCmd{Refs: []string{"1"}}).Run(c)
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(w.unarchived) != 0 {
		t.Errorf("a current page must not be submitted, got %v", w.unarchived)
	}
	if row := lines[0]; row["unarchived"] != false || row["status"] != "current" {
		t.Errorf("no-op row unexpected: %v", row)
	}
}

func TestPageUnarchive_FolderIsNotAPage(t *testing.T) {
	w := &archiveWorld{folders: map[string]bool{"1": true}}
	lines, err := runArchive(t, w, func(c *CLI) error {
		return (&PageUnarchiveCmd{Refs: []string{"1"}}).Run(c)
	})
	if !errors.As(err, new(*output.ExitError)) {
		t.Fatalf("expected *output.ExitError, got %T: %v", err, err)
	}
	if lines[0]["error"] != "not_a_page" {
		t.Errorf("row = %v, want not_a_page", lines[0])
	}
}

func TestPageUnarchive_MutationFailureInline(t *testing.T) {
	w := &archiveWorld{
		statuses: map[string][]string{"1": {"archived"}, "2": {"archived"}},
		gqlFail:  map[string]bool{"1": true},
	}
	lines, err := runArchive(t, w, func(c *CLI) error {
		return (&PageUnarchiveCmd{Refs: []string{"1", "2"}}).Run(c)
	})
	if !errors.As(err, new(*output.ExitError)) {
		t.Fatalf("expected *output.ExitError, got %T: %v", err, err)
	}
	if lines[0]["error"] != "unarchive_failed" || lines[0]["input"] != "1" {
		t.Errorf("row = %v, want inline unarchive_failed", lines[0])
	}
	if lines[1]["unarchived"] != true {
		t.Errorf("a failed mutation must not stop later pages: %v", lines[1])
	}
}

func TestPageUnarchive_TaskFailure(t *testing.T) {
	w := &archiveWorld{
		statuses: map[string][]string{"1": {"archived"}},
		tasks:    map[string][]string{"1": {taskFailed}},
	}
	lines, _ := runArchive(t, w, func(c *CLI) error {
		return (&PageUnarchiveCmd{Refs: []string{"1"}}).Run(c)
	})
	if lines[0]["error"] != "unarchive_failed" {
		t.Errorf("row = %v, want unarchive_failed", lines[0])
	}
}

func TestPageUnarchive_TimeoutAfterSubmitIsPending(t *testing.T) {
	w := &archiveWorld{
		statuses: map[string][]string{"1": {"archived"}, "2": {"archived"}},
		tasks:    map[string][]string{"1": {taskRunning}},
	}
	lines, err := runArchive(t, w, func(c *CLI) error {
		c.Timeout = 100 * time.Millisecond
		return (&PageUnarchiveCmd{Refs: []string{"1", "2"}}).Run(c)
	})
	var exitErr *output.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != output.ExitNetwork {
		t.Fatalf("error = %v, want *output.ExitError with the timeout code", err)
	}
	if lines[0]["error"] != "unarchive_pending" {
		t.Fatalf("row = %v, want unarchive_pending", lines[0])
	}
	if strings.Join(w.unarchived, ",") != "1" {
		t.Errorf("unarchived = %v, want page 2 never submitted", w.unarchived)
	}
}

// A poll that fails after the submit succeeded leaves the outcome unknown, so
// the page is pending and the batch stops.
func TestPageArchive_PollErrorAfterSubmitIsPending(t *testing.T) {
	w := &archiveWorld{tasks: map[string][]string{"1": {taskGone}}}
	lines, err := runArchive(t, w, func(c *CLI) error {
		return (&PageArchiveCmd{Refs: []string{"1", "2"}}).Run(c)
	})
	var exitErr *output.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *output.ExitError, got %T: %v", err, err)
	}
	if lines[0]["error"] != "archive_pending" {
		t.Fatalf("row = %v, want archive_pending", lines[0])
	}
	if data, _ := lines[0]["data"].(map[string]any); data["task_id"] != "t-1" {
		t.Errorf("data = %v, want task_id t-1", data)
	}
	if len(lines) != 2 || strings.Join(w.archived, ",") != "1" {
		t.Errorf("lines = %v, archived = %v, want the batch stopped after page 1", lines, w.archived)
	}
}

// A deadline that expires before any submit stops the batch at the first page
// instead of failing every remaining ref with the same timeout.
func TestPageArchive_TimeoutBeforeSubmitStopsBatch(t *testing.T) {
	w := &archiveWorld{}
	lines, err := runArchive(t, w, func(c *CLI) error {
		c.Timeout = time.Nanosecond
		return (&PageArchiveCmd{Refs: []string{"1", "2", "3"}}).Run(c)
	})
	var exitErr *output.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *output.ExitError, got %T: %v", err, err)
	}
	if exitErr.Code != output.ExitNetwork {
		t.Errorf("exit code = %d, want %d (timeout)", exitErr.Code, output.ExitNetwork)
	}
	if len(lines) != 2 || lines[0]["input"] != "1" {
		t.Errorf("lines = %v, want one error row for page 1 and the trailer", lines)
	}
	if len(w.archived) != 0 {
		t.Errorf("archived = %v, want none", w.archived)
	}
}
