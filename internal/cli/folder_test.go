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

	"github.com/tammersaleh/confluence-cli/internal/output"
)

// folderFixture is the v2 folder response for folder id 777 under page 100 in
// space s1. createdAt is epoch milliseconds, as the live API returns it.
const folderFixture = `{
	"id": "777", "type": "folder", "title": "Pillars",
	"parentId": "100", "parentType": "page",
	"spaceId": "s1", "status": "current", "authorId": "user1",
	"createdAt": 1790626470759,
	"version": {"number": 1, "createdAt": "2026-09-28T20:14:30.759Z"},
	"_links": {"webui": "/spaces/T/folder/777"}
}`

// folderWorld is a fake site with one page (100, space s1, version 5, parent
// 50) and one folder (777, under page 100). It records every request path so
// tests can assert which endpoints were probed. Pages and folders not listed
// 404 on every endpoint.
type folderWorld struct {
	mu       sync.Mutex
	paths    []string
	deleted  []string
	created  map[string]any
	v1Moves  []string
	v2Puts   int
	pageForb bool // when set, GET /pages/100 returns 403
	cycle    bool // when set, folder 777 reports page 100 among its ancestors
}

func (fw *folderWorld) record(p string) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.paths = append(fw.paths, p)
}

func (fw *folderWorld) count(substr string) int {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	n := 0
	for _, p := range fw.paths {
		if strings.Contains(p, substr) {
			n++
		}
	}
	return n
}

func folderServer(t *testing.T, fw *folderWorld) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fw.record(r.Method + " " + r.URL.Path)
		p := r.URL.Path
		switch {
		case p == "/wiki/api/v2/spaces":
			_, _ = w.Write([]byte(`{"results":[{"id":"s1","key":"ENG","name":"Engineering"}]}`))
		case p == "/wiki/api/v2/pages" && r.Method == http.MethodPost:
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			fw.mu.Lock()
			fw.created = payload
			fw.mu.Unlock()
			_, _ = w.Write([]byte(`{"id":"555","title":"New","spaceId":"s1","parentId":"777","version":{"number":1},"_links":{"webui":"/spaces/T/pages/555/N"}}`))
		case p == "/wiki/api/v2/folders" && r.Method == http.MethodPost:
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			fw.mu.Lock()
			fw.created = payload
			fw.mu.Unlock()
			_, _ = w.Write([]byte(folderFixture))
		case p == "/wiki/api/v2/folders/777" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(folderFixture))
		case p == "/wiki/api/v2/folders/777" && r.Method == http.MethodDelete:
			fw.mu.Lock()
			fw.deleted = append(fw.deleted, "777")
			fw.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case p == "/wiki/api/v2/folders/777/direct-children":
			if r.URL.Query().Get("cursor") == "C2" {
				_, _ = w.Write([]byte(`{"results":[{"id":"12","title":"In Folder B","type":"page"}],"_links":{}}`))
				return
			}
			_, _ = w.Write([]byte(`{"results":[{"id":"11","title":"In Folder A","type":"page"}],"_links":{"next":"/wiki/api/v2/folders/777/direct-children?cursor=C2"}}`))
		case p == "/wiki/api/v2/folders/777/ancestors":
			anc := `{"id":"1","type":"page"},{"id":"50","type":"page"}`
			if fw.cycle {
				anc = `{"id":"1","type":"page"},{"id":"100","type":"page"}`
			}
			_, _ = w.Write([]byte(`{"results":[` + anc + `],"_links":{}}`))
		case p == "/wiki/api/v2/pages/100" && r.Method == http.MethodGet:
			if fw.pageForb {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = w.Write([]byte(`{
				"id":"100","title":"Source","spaceId":"s1","status":"current","parentId":"50","parentType":"page",
				"authorId":"user1","createdAt":"2024-01-01T00:00:00Z",
				"body":{"atlas_doc_format":{"value":"{\"type\":\"doc\",\"version\":1,\"content\":[]}"},"storage":{"value":"<p>x</p>"}},
				"version":{"number":5,"createdAt":"2024-06-20T00:00:00Z"},
				"_links":{"webui":"/spaces/T/pages/100/S"}
			}`))
		case p == "/wiki/api/v2/pages/100" && r.Method == http.MethodPut:
			fw.mu.Lock()
			fw.v2Puts++
			fw.mu.Unlock()
			_, _ = w.Write([]byte(`{"id":"100","title":"Source","spaceId":"s1","version":{"number":6},"_links":{"webui":"/spaces/T/pages/100/S"}}`))
		case p == "/wiki/api/v2/pages/100/direct-children":
			if fw.pageForb {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = w.Write([]byte(`{"results":[{"id":"777","title":"Pillars","type":"folder"},{"id":"101","title":"Child","type":"page"}],"_links":{}}`))
		case strings.HasPrefix(p, "/wiki/rest/api/content/100/move/append/"):
			fw.mu.Lock()
			fw.v1Moves = append(fw.v1Moves, strings.TrimPrefix(p, "/wiki/rest/api/content/100/move/append/"))
			fw.mu.Unlock()
			_, _ = w.Write([]byte(`{"pageId":"100"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"no such content"}`))
		}
	}))
}

func newFolderCLI(t *testing.T, fw *folderWorld) (*CLI, *bytes.Buffer, *bytes.Buffer, *httptest.Server) {
	t.Helper()
	clearCredEnv(t)
	server := folderServer(t, fw)
	t.Cleanup(server.Close)
	setEnvCreds(t, server.URL)
	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "")
	return c, &out, &errBuf, server
}

func outputErr(t *testing.T, err error) *output.Error {
	t.Helper()
	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	return oErr
}

// --- page children ---

func TestPageChildren_PageListsFoldersToo(t *testing.T) {
	fw := &folderWorld{}
	c, out, _, _ := newFolderCLI(t, fw)

	if err := (&PageChildrenCmd{Ref: "100", Limit: 25}).Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	lines := parseLines(t, out.String())
	if len(lines) != 3 {
		t.Fatalf("expected 2 rows + meta, got %q", out.String())
	}
	if lines[0]["id"] != "777" || lines[0]["type"] != "folder" || lines[1]["type"] != "page" {
		t.Errorf("rows = %v %v", lines[0], lines[1])
	}
	if fw.count("/folders/") != 0 {
		t.Errorf("a page id must not probe the folders endpoint: %v", fw.paths)
	}
}

func TestPageChildren_BareFolderIDFallsBack(t *testing.T) {
	fw := &folderWorld{}
	c, out, _, _ := newFolderCLI(t, fw)

	if err := (&PageChildrenCmd{Ref: "777", Limit: 25, All: true}).Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	lines := parseLines(t, out.String())
	if len(lines) != 3 || lines[0]["id"] != "11" || lines[1]["id"] != "12" {
		t.Fatalf("rows unexpected: %q", out.String())
	}
	// One page probe (404), then the folder endpoint for every page of results.
	if fw.count("/pages/777/direct-children") != 1 {
		t.Errorf("page endpoint probed %d times, want 1: %v", fw.count("/pages/777/direct-children"), fw.paths)
	}
	if fw.count("/folders/777/direct-children") != 2 {
		t.Errorf("folder endpoint hit %d times, want 2: %v", fw.count("/folders/777/direct-children"), fw.paths)
	}
}

func TestPageChildren_FolderURLSkipsPageProbe(t *testing.T) {
	fw := &folderWorld{}
	c, out, _, server := newFolderCLI(t, fw)

	if err := (&PageChildrenCmd{Ref: server.URL + "/wiki/spaces/T/folder/777", Limit: 25}).Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	lines := parseLines(t, out.String())
	if len(lines) != 2 || lines[0]["id"] != "11" {
		t.Fatalf("rows unexpected: %q", out.String())
	}
	if fw.count("/pages/") != 0 {
		t.Errorf("folder URL must not touch the pages endpoint: %v", fw.paths)
	}
	meta := lines[1]["_meta"].(map[string]any)
	if meta["next_cursor"] != "C2" {
		t.Errorf("meta = %v", meta)
	}
}

func TestPageChildren_ForbiddenDoesNotFallBack(t *testing.T) {
	fw := &folderWorld{pageForb: true}
	c, _, _, _ := newFolderCLI(t, fw)

	err := (&PageChildrenCmd{Ref: "100", Limit: 25}).Run(c)
	if oErr := outputErr(t, err); oErr.Err != "forbidden" {
		t.Errorf("Err = %q, want forbidden", oErr.Err)
	}
	if fw.count("/folders/") != 0 {
		t.Errorf("403 must not trigger the folder fallback: %v", fw.paths)
	}
}

func TestPageChildren_NeitherFound(t *testing.T) {
	fw := &folderWorld{}
	c, _, _, _ := newFolderCLI(t, fw)

	err := (&PageChildrenCmd{Ref: "999", Limit: 25}).Run(c)
	if oErr := outputErr(t, err); oErr.Err != "page_not_found" {
		t.Errorf("Err = %q, want page_not_found", oErr.Err)
	}
}

// --- page get ---

func TestPageGet_BareFolderIDIsNotAPage(t *testing.T) {
	fw := &folderWorld{}
	c, out, _, _ := newFolderCLI(t, fw)

	err := (&PageGetCmd{Refs: []string{"777"}, BodyFormat: "storage"}).Run(c)
	var exitErr *output.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError for the inline failure, got %v", err)
	}
	lines := parseLines(t, out.String())
	if len(lines) != 2 {
		t.Fatalf("expected 1 inline error + meta: %q", out.String())
	}
	if lines[0]["error"] != "not_a_page" || lines[0]["input"] != "777" {
		t.Errorf("row = %v", lines[0])
	}
	if hint, _ := lines[0]["hint"].(string); !strings.Contains(hint, "folder get 777") {
		t.Errorf("hint should point at folder get: %v", lines[0])
	}
	if fw.count("GET /wiki/api/v2/folders/777") != 1 {
		t.Errorf("expected one folder probe: %v", fw.paths)
	}
}

func TestPageGet_UnknownIDStaysPageNotFound(t *testing.T) {
	fw := &folderWorld{}
	c, out, _, _ := newFolderCLI(t, fw)

	_ = (&PageGetCmd{Refs: []string{"999"}, BodyFormat: "storage"}).Run(c)
	lines := parseLines(t, out.String())
	if lines[0]["error"] != "page_not_found" {
		t.Errorf("row = %v", lines[0])
	}
}

func TestPageGet_FolderURLRejectedLocally(t *testing.T) {
	fw := &folderWorld{}
	c, _, _, server := newFolderCLI(t, fw)

	err := (&PageGetCmd{Refs: []string{server.URL + "/wiki/spaces/T/folder/777"}, BodyFormat: "storage"}).Run(c)
	oErr := outputErr(t, err)
	if oErr.Err != "invalid_input" || !strings.Contains(oErr.Hint, "folder get") {
		t.Errorf("err = %+v", oErr)
	}
	if len(fw.paths) != 0 {
		t.Errorf("a folder URL must fail before any request: %v", fw.paths)
	}
}

// --- page move ---

func TestPageMove_IntoFolderUsesV1(t *testing.T) {
	fw := &folderWorld{}
	c, out, _, _ := newFolderCLI(t, fw)

	if err := (&PageMoveCmd{Ref: "100", Parent: "777", IfVersion: 5}).Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(fw.v1Moves) != 1 || fw.v1Moves[0] != "777" {
		t.Fatalf("v1 move calls = %v, want [777]", fw.v1Moves)
	}
	if fw.v2Puts != 0 {
		t.Errorf("a move into a folder must not PUT the v2 page")
	}
	r := parseLines(t, out.String())[0]
	if r["moved"] != true || r["parent_id"] != "777" || r["parent_type"] != "folder" || r["previous_parent_id"] != "50" {
		t.Errorf("row = %v", r)
	}
	if r["space_id"] != "s1" || r["previous_space_id"] != "s1" {
		t.Errorf("space ids = %v", r)
	}
	// The v1 move does not bump the version.
	if r["version"] != float64(5) || r["previous_version"] != float64(5) {
		t.Errorf("version must be unchanged: %v", r)
	}
}

func TestPageMove_IntoFolderURL(t *testing.T) {
	fw := &folderWorld{}
	c, out, _, server := newFolderCLI(t, fw)

	if err := (&PageMoveCmd{Ref: "100", Parent: server.URL + "/wiki/spaces/T/folder/777", IfVersion: 5}).Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if fw.count("GET /wiki/api/v2/pages/777") != 0 {
		t.Errorf("folder URL must not probe /pages/777: %v", fw.paths)
	}
	if parseLines(t, out.String())[0]["parent_type"] != "folder" {
		t.Errorf("row = %v", out.String())
	}
}

func TestPageMove_IntoFolderCycleRefused(t *testing.T) {
	fw := &folderWorld{cycle: true}
	c, _, _, server := newFolderCLI(t, fw)

	// Folder 777 reports page 100 among its ancestors, so moving 100 under 777 is a cycle.
	// Use a folder URL so the move goes straight to the folder endpoints.
	err := (&PageMoveCmd{Ref: "100", Parent: server.URL + "/wiki/spaces/T/folder/777", IfVersion: 5}).Run(c)
	if oErr := outputErr(t, err); oErr.Err != "invalid_move" {
		t.Errorf("Err = %q, want invalid_move", oErr.Err)
	}
	if len(fw.v1Moves) != 0 {
		t.Errorf("cycle must not move: %v", fw.v1Moves)
	}
}

func TestPageMove_ParentTypeOnPageRows(t *testing.T) {
	clearCredEnv(t)
	var cap capturedUpdate
	adf := `{"type":"doc","version":1,"content":[]}`
	server := pageMoveServer(t, moveServerConfig{
		srcID: "100", dstID: "200", srcVersion: 5, srcParentID: "50",
		srcSpaceID: "s1", srcBody: adf, dstSpaceID: "s1",
	}, &cap)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "")
	if err := (&PageMoveCmd{Ref: "100", Parent: "200", IfVersion: 5}).Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if r := parseLines(t, out.String())[0]; r["parent_type"] != "page" {
		t.Errorf("row = %v", r)
	}
}

// --- folder get ---

func TestFolderGet_BareID(t *testing.T) {
	fw := &folderWorld{}
	c, out, _, server := newFolderCLI(t, fw)

	if err := (&FolderGetCmd{Refs: []string{"777"}}).Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	lines := parseLines(t, out.String())
	if len(lines) != 2 {
		t.Fatalf("expected row + meta: %q", out.String())
	}
	r := lines[0]
	want := map[string]any{
		"input": "777", "id": "777", "type": "folder", "title": "Pillars",
		"space_id": "s1", "status": "current", "parent_id": "100", "parent_type": "page",
		"version": float64(1), "author_id": "user1",
		"created_at": "2026-09-28T20:14:30.759Z", "modified_at": "2026-09-28T20:14:30.759Z",
		"web_url": server.URL + "/wiki/spaces/T/folder/777",
	}
	for k, v := range want {
		if r[k] != v {
			t.Errorf("%s = %v, want %v", k, r[k], v)
		}
	}
	if _, ok := r["body"]; ok {
		t.Errorf("folders have no body: %v", r)
	}
	if r["created_at_iso"] != "2026-09-28T20:14:30Z" {
		t.Errorf("created_at_iso = %v", r["created_at_iso"])
	}
}

func TestFolderGet_URLAndPerItemError(t *testing.T) {
	fw := &folderWorld{}
	c, out, _, server := newFolderCLI(t, fw)

	err := (&FolderGetCmd{Refs: []string{server.URL + "/wiki/spaces/T/folder/777", "999"}}).Run(c)
	var exitErr *output.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %v", err)
	}
	lines := parseLines(t, out.String())
	if len(lines) != 3 || lines[0]["id"] != "777" {
		t.Fatalf("lines = %q", out.String())
	}
	if lines[1]["error"] != "folder_not_found" || lines[1]["input"] != "999" {
		t.Errorf("inline error = %v", lines[1])
	}
	if meta := lines[2]["_meta"].(map[string]any); meta["error_count"] != float64(1) {
		t.Errorf("meta = %v", meta)
	}
}

func TestFolderGet_PageURLRejected(t *testing.T) {
	fw := &folderWorld{}
	c, _, _, server := newFolderCLI(t, fw)

	err := (&FolderGetCmd{Refs: []string{server.URL + "/wiki/spaces/T/pages/100"}}).Run(c)
	if oErr := outputErr(t, err); oErr.Err != "invalid_input" {
		t.Errorf("err = %+v", oErr)
	}
}

// --- folder create ---

func TestFolderCreate_WithParent(t *testing.T) {
	fw := &folderWorld{}
	c, out, _, _ := newFolderCLI(t, fw)

	if err := (&FolderCreateCmd{Space: "ENG", Title: "Pillars", Parent: "100"}).Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if fw.created["spaceId"] != "s1" || fw.created["title"] != "Pillars" || fw.created["parentId"] != "100" {
		t.Errorf("payload = %v", fw.created)
	}
	r := parseLines(t, out.String())[0]
	if r["id"] != "777" || r["type"] != "folder" || r["parent_id"] != "100" || r["version"] != float64(1) {
		t.Errorf("row = %v", r)
	}
	if _, ok := r["input"]; ok {
		t.Errorf("create rows do not echo input: %v", r)
	}
}

func TestFolderCreate_NoParent_FolderURLParent(t *testing.T) {
	fw := &folderWorld{}
	c, _, _, server := newFolderCLI(t, fw)

	if err := (&FolderCreateCmd{Space: "ENG", Title: "Root"}).Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if _, ok := fw.created["parentId"]; ok {
		t.Errorf("parentId must be omitted: %v", fw.created)
	}

	// A folder URL is a valid parent (folders nest).
	if err := (&FolderCreateCmd{Space: "ENG", Title: "Nested", Parent: server.URL + "/wiki/spaces/T/folder/777"}).Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if fw.created["parentId"] != "777" {
		t.Errorf("payload = %v", fw.created)
	}
}

// --- folder delete ---

func TestFolderDelete_NoYes(t *testing.T) {
	fw := &folderWorld{}
	c, _, _, _ := newFolderCLI(t, fw)

	err := (&FolderDeleteCmd{Refs: []string{"777"}}).Run(c)
	if oErr := outputErr(t, err); oErr.Err != "confirmation_required" {
		t.Errorf("Err = %q", oErr.Err)
	}
	if len(fw.deleted) != 0 {
		t.Errorf("nothing may be deleted without --yes")
	}
}

func TestFolderDelete_DryRun(t *testing.T) {
	fw := &folderWorld{}
	c, out, _, _ := newFolderCLI(t, fw)

	if err := (&FolderDeleteCmd{Refs: []string{"777"}, DryRun: true}).Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	r := parseLines(t, out.String())[0]
	if r["input"] != "777" || r["id"] != "777" || r["title"] != "Pillars" || r["would_delete"] != true || r["delete_mode"] != "trash" {
		t.Errorf("row = %v", r)
	}
	if len(fw.deleted) != 0 {
		t.Errorf("dry-run must not delete")
	}
}

func TestFolderDelete_WithYesAndPerItem404(t *testing.T) {
	fw := &folderWorld{}
	c, out, _, _ := newFolderCLI(t, fw)

	err := (&FolderDeleteCmd{Refs: []string{"777", "999"}, Yes: true}).Run(c)
	var exitErr *output.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError for partial failure, got %v", err)
	}
	lines := parseLines(t, out.String())
	if lines[0]["deleted"] != true || lines[0]["delete_mode"] != "trash" || lines[0]["id"] != "777" {
		t.Errorf("row = %v", lines[0])
	}
	if lines[1]["error"] != "folder_not_found" {
		t.Errorf("inline = %v", lines[1])
	}
	if len(fw.deleted) != 1 {
		t.Errorf("deleted = %v", fw.deleted)
	}
}

// --- page create with a folder parent ---

func TestPageCreate_FolderURLParent(t *testing.T) {
	fw := &folderWorld{}
	c, out, _, server := newFolderCLI(t, fw)

	// POST /pages accepts a folder parentId (verified live), so a folder URL is
	// a valid --parent for page create.
	cmd := &PageCreateCmd{Space: "ENG", Title: "New", Parent: server.URL + "/wiki/spaces/T/folder/777"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if fw.created["parentId"] != "777" {
		t.Errorf("payload = %v", fw.created)
	}
	if r := parseLines(t, out.String())[0]; r["parent_id"] != "777" {
		t.Errorf("row = %v", r)
	}
}
