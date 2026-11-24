package confluence

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClient_ArchivePage_Success(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotBody   map[string]any
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"id":"777","links":{"status":"/rest/api/longtask/777"}}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	taskID, err := c.ArchivePage(context.Background(), "12345")
	if err != nil {
		t.Fatalf("ArchivePage() error: %v", err)
	}
	if taskID != "777" {
		t.Errorf("taskID = %q, want 777", taskID)
	}
	if gotMethod != http.MethodPost || gotPath != "/wiki/rest/api/content/archive" {
		t.Errorf("request = %s %s, want POST /wiki/rest/api/content/archive", gotMethod, gotPath)
	}
	pages, _ := gotBody["pages"].([]any)
	if len(pages) != 1 {
		t.Fatalf("pages = %v, want exactly one entry", gotBody["pages"])
	}
	if first, _ := pages[0].(map[string]any); first["id"] != "12345" {
		t.Errorf("pages[0].id = %v, want 12345", first["id"])
	}
}

func TestClient_ArchivePage_MissingTaskID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"links":{}}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	if _, err := c.ArchivePage(context.Background(), "1"); !errors.Is(err, ErrAPIError) {
		t.Fatalf("error = %v, want ErrAPIError", err)
	}
}

// The archive endpoint's 400 carries the useful reason in
// data.errors[].message.key; the top-level message is a generic exception.
func TestClient_ArchivePage_BadRequestCarriesReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"statusCode":400,"data":{"authorized":true,"valid":false,"errors":[{"message":{"key":"Cannot archive pages because some or all pages in the input are already archived.","args":[]}}],"successful":false},"message":"com.atlassian.confluence.api.service.exceptions.api.BadRequestException: Incorrect arguments. One or more given Content IDs were not valid"}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	_, err := c.ArchivePage(context.Background(), "1")
	if !errors.Is(err, ErrAPIError) {
		t.Fatalf("error = %v, want ErrAPIError", err)
	}
	if !strings.Contains(err.Error(), "already archived") {
		t.Errorf("error %q should carry the server's reason", err.Error())
	}
}

func TestClient_GetLongTask(t *testing.T) {
	cases := []struct {
		name string
		body string
		want LongTask
	}{
		{
			name: "running",
			body: `{"id":"777","percentageComplete":0,"successful":false,"finished":false,"messages":[{"translation":"","args":[]}],"status":"RUNNING","errors":[]}`,
			want: LongTask{ID: "777", Status: "RUNNING"},
		},
		{
			name: "finished",
			body: `{"id":"777","percentageComplete":100,"successful":true,"finished":true,"messages":[{"translation":"PageArchivedList :: [12345]","args":[]}],"status":"FINISH_SUCCESS","errors":[]}`,
			want: LongTask{ID: "777", Finished: true, Successful: true, Status: "FINISH_SUCCESS", Messages: []string{"PageArchivedList :: [12345]"}},
		},
		{
			name: "failed",
			body: `{"id":"777","successful":false,"finished":true,"messages":[],"status":"FINISH_FAILED","errors":[{"message":"boom"}]}`,
			want: LongTask{ID: "777", Finished: true, Status: "FINISH_FAILED", Errors: []string{`{"message":"boom"}`}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.Write([]byte(tc.body))
			}))
			defer server.Close()

			c := NewClient(server.URL, "test@example.com", "api-token")
			got, err := c.GetLongTask(context.Background(), "777")
			if err != nil {
				t.Fatalf("GetLongTask() error: %v", err)
			}
			if gotPath != "/wiki/rest/api/longtask/777" {
				t.Errorf("path = %q, want /wiki/rest/api/longtask/777", gotPath)
			}
			if got.ID != tc.want.ID || got.Finished != tc.want.Finished || got.Successful != tc.want.Successful || got.Status != tc.want.Status {
				t.Errorf("task = %+v, want %+v", *got, tc.want)
			}
			if strings.Join(got.Messages, "|") != strings.Join(tc.want.Messages, "|") {
				t.Errorf("Messages = %v, want %v", got.Messages, tc.want.Messages)
			}
			if strings.Join(got.Errors, "|") != strings.Join(tc.want.Errors, "|") {
				t.Errorf("Errors = %v, want %v", got.Errors, tc.want.Errors)
			}
		})
	}
}

func TestClient_GetLongTask_MalformedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`not json`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	if _, err := c.GetLongTask(context.Background(), "777"); err == nil {
		t.Fatal("expected a decode error")
	}
}

func TestClient_UnarchivePage_Success(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotBody   map[string]any
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"data":{"bulkUnarchivePages":{"status":true,"taskId":"888"}}}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	taskID, err := c.UnarchivePage(context.Background(), "12345")
	if err != nil {
		t.Fatalf("UnarchivePage() error: %v", err)
	}
	if taskID != "888" {
		t.Errorf("taskID = %q, want 888", taskID)
	}
	if gotMethod != http.MethodPost || gotPath != "/gateway/api/graphql" {
		t.Errorf("request = %s %s, want POST /gateway/api/graphql", gotMethod, gotPath)
	}
	// The gateway warns on unnamed operations, as a top-level error.
	if gotBody["operationName"] != "UnarchivePage" {
		t.Errorf("operationName = %v, want UnarchivePage", gotBody["operationName"])
	}
	q, _ := gotBody["query"].(string)
	if !strings.Contains(q, "mutation UnarchivePage(") {
		t.Errorf("query is not the named operation: %q", q)
	}
	if strings.Contains(q, "12345") {
		t.Errorf("query interpolates the page id: %q", q)
	}
	vars, _ := gotBody["variables"].(map[string]any)
	ids, _ := vars["ids"].([]any)
	if len(ids) != 1 || ids[0] != float64(12345) {
		t.Errorf("variables.ids = %v, want [12345] as a number", vars["ids"])
	}
	// includeChildren needs exactly one entry per page id.
	inc, _ := vars["includeChildren"].([]any)
	if len(inc) != 1 || inc[0] != false {
		t.Errorf("variables.includeChildren = %v, want [false]", vars["includeChildren"])
	}
}

func TestClient_UnarchivePage_NonNumericID(t *testing.T) {
	c := NewClient("http://127.0.0.1:0", "test@example.com", "api-token")
	if _, err := c.UnarchivePage(context.Background(), "abc"); !errors.Is(err, ErrAPIError) {
		t.Fatalf("error = %v, want ErrAPIError before any request", err)
	}
}

func TestClient_UnarchivePage_LogicalFailures(t *testing.T) {
	cases := map[string]string{
		"top-level errors":       `{"errors":[{"message":"BadRequestException: Incorrect arguments.","extensions":{"statusCode":400,"data":{"errors":[{"message":{"key":"Cannot unarchive pages because some or all pages in the input aren't archived."}}]}}}],"data":{"bulkUnarchivePages":null}}`,
		"errors beside success":  `{"errors":[{"message":"warning"}],"data":{"bulkUnarchivePages":{"status":true,"taskId":"888"}}}`,
		"null payload":           `{"data":{"bulkUnarchivePages":null}}`,
		"missing data":           `{}`,
		"status false":           `{"data":{"bulkUnarchivePages":{"status":false,"taskId":"888"}}}`,
		"status absent":          `{"data":{"bulkUnarchivePages":{"taskId":"888"}}}`,
		"no task id":             `{"data":{"bulkUnarchivePages":{"status":true}}}`,
		"malformed":              `not json`,
		"trailing junk":          `{"data":{"bulkUnarchivePages":{"status":true,"taskId":"888"}}} junk`,
		"wrong mutation in data": `{"data":{"somethingElse":{"status":true,"taskId":"888"}}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(body))
			}))
			defer server.Close()

			c := NewClient(server.URL, "test@example.com", "api-token")
			_, err := c.UnarchivePage(context.Background(), "1")
			if !errors.Is(err, ErrUnarchiveFailed) {
				t.Fatalf("error = %v, want ErrUnarchiveFailed", err)
			}
		})
	}
}

func TestClient_UnarchivePage_FailureCarriesReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"errors":[{"message":"BadRequestException: Incorrect arguments.","extensions":{"statusCode":400,"data":{"errors":[{"message":{"key":"Cannot unarchive pages because some or all pages in the input aren't archived."}}]}}}],"data":{"bulkUnarchivePages":null}}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	_, err := c.UnarchivePage(context.Background(), "1")
	if err == nil || !strings.Contains(err.Error(), "aren't archived") {
		t.Errorf("error %v should carry the server's reason", err)
	}
}

func TestClient_UnarchivePage_StatusErrorsPreserved(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	_, err := c.UnarchivePage(context.Background(), "1")
	if !errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrUnarchiveFailed) {
		t.Fatalf("error = %v, want ErrUnauthorized and not ErrUnarchiveFailed", err)
	}
}
