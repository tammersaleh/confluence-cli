package confluence

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

const folderJSON = `{
	"id": "777",
	"type": "folder",
	"parentId": "100",
	"parentType": "page",
	"title": "Pillars",
	"authorId": "user1",
	"createdAt": 1790626470759,
	"version": {"number": 1, "createdAt": "2026-09-28T20:14:30.759Z"},
	"status": "current",
	"spaceId": "space1",
	"_links": {"webui": "/spaces/ENG/folder/777"}
}`

func TestClient_GetFolder(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(folderJSON))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	f, err := c.GetFolder(context.Background(), "777")
	if err != nil {
		t.Fatalf("GetFolder() error: %v", err)
	}
	if gotPath != "/wiki/api/v2/folders/777" {
		t.Errorf("path = %q", gotPath)
	}
	if f.ID != "777" || f.Title != "Pillars" || f.SpaceID != "space1" || f.Status != "current" {
		t.Errorf("folder = %+v", f)
	}
	if f.ParentID != "100" || f.ParentType != "page" {
		t.Errorf("parent = %q/%q, want 100/page", f.ParentID, f.ParentType)
	}
	if f.Version != 1 || f.AuthorID != "user1" {
		t.Errorf("version/author = %d/%q", f.Version, f.AuthorID)
	}
	// The folders endpoint returns createdAt as epoch milliseconds; the client
	// normalizes it to ISO 8601 like every other timestamp.
	if f.CreatedAt != "2026-09-28T20:14:30.759Z" {
		t.Errorf("CreatedAt = %q, want 2026-09-28T20:14:30.759Z", f.CreatedAt)
	}
	if f.ModifiedAt != "2026-09-28T20:14:30.759Z" {
		t.Errorf("ModifiedAt = %q", f.ModifiedAt)
	}
	if want := server.URL + "/wiki/spaces/ENG/folder/777"; f.WebURL != want {
		t.Errorf("WebURL = %q, want %q", f.WebURL, want)
	}
}

func TestClient_GetFolder_NumericVersionCreatedAt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"1","title":"F","createdAt":1790626470759,"version":{"number":2,"createdAt":1790626470759}}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	f, err := c.GetFolder(context.Background(), "1")
	if err != nil {
		t.Fatalf("GetFolder() error: %v", err)
	}
	if f.ModifiedAt != "2026-09-28T20:14:30.759Z" {
		t.Errorf("ModifiedAt = %q", f.ModifiedAt)
	}
}

func TestClient_GetFolder_ISOCreatedAt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"1","title":"F","createdAt":"2024-01-15T10:30:00.000Z","version":{"number":2}}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	f, err := c.GetFolder(context.Background(), "1")
	if err != nil {
		t.Fatalf("GetFolder() error: %v", err)
	}
	if f.CreatedAt != "2024-01-15T10:30:00.000Z" {
		t.Errorf("CreatedAt = %q", f.CreatedAt)
	}
}

func TestClient_GetFolder_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	_, err := c.GetFolder(context.Background(), "777")
	if !errors.Is(err, ErrFolderNotFound) {
		t.Fatalf("expected ErrFolderNotFound, got %v", err)
	}
	if errors.Is(err, ErrPageNotFound) {
		t.Fatalf("ErrFolderNotFound must not match ErrPageNotFound")
	}
	// The transport error is kept so the CLI can report the endpoint.
	var se *StatusError
	if !errors.As(err, &se) || se.Endpoint != "/wiki/api/v2/folders/777" || se.StatusCode != 404 {
		t.Fatalf("expected StatusError for /wiki/api/v2/folders/777, got %#v", err)
	}
}

func TestClient_ListDirectChildren_Page(t *testing.T) {
	var gotPath, gotLimit string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotLimit = r.URL.Query().Get("limit")
		w.Write([]byte(`{
			"results": [
				{"id": "1", "title": "Child One", "status": "current", "type": "page", "childPosition": 0},
				{"id": "2", "title": "Sub", "status": "current", "type": "folder", "childPosition": 1}
			],
			"_links": {"next": "/wiki/api/v2/pages/100/direct-children?limit=25&cursor=NEXT"}
		}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	items, next, err := c.ListDirectChildren(context.Background(), ContentPage, "100", "", 0)
	if err != nil {
		t.Fatalf("ListDirectChildren() error: %v", err)
	}
	if gotPath != "/wiki/api/v2/pages/100/direct-children" {
		t.Errorf("path = %q", gotPath)
	}
	if gotLimit != "25" {
		t.Errorf("limit = %q, want 25 (default)", gotLimit)
	}
	if len(items) != 2 || items[0].ID != "1" || items[0].Type != "page" || items[1].Type != "folder" || items[1].Title != "Sub" {
		t.Errorf("items = %+v", items)
	}
	if next != "NEXT" {
		t.Errorf("next = %q", next)
	}
}

func TestClient_ListDirectChildren_Folder(t *testing.T) {
	var gotPath, gotCursor string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotCursor = r.URL.Query().Get("cursor")
		w.Write([]byte(`{"results": [{"id": "9", "title": "In Folder", "type": "page"}], "_links": {}}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	items, next, err := c.ListDirectChildren(context.Background(), ContentFolder, "777", "C1", 10)
	if err != nil {
		t.Fatalf("ListDirectChildren() error: %v", err)
	}
	if gotPath != "/wiki/api/v2/folders/777/direct-children" || gotCursor != "C1" {
		t.Errorf("path/cursor = %q/%q", gotPath, gotCursor)
	}
	if len(items) != 1 || items[0].ID != "9" || next != "" {
		t.Errorf("items = %+v next = %q", items, next)
	}
}

func TestClient_ListDirectChildren_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	_, _, err := c.ListDirectChildren(context.Background(), ContentPage, "100", "", 25)
	if !errors.Is(err, ErrPageNotFound) {
		t.Fatalf("page kind: expected ErrPageNotFound, got %v", err)
	}
	_, _, err = c.ListDirectChildren(context.Background(), ContentFolder, "100", "", 25)
	if !errors.Is(err, ErrFolderNotFound) {
		t.Fatalf("folder kind: expected ErrFolderNotFound, got %v", err)
	}
}

func TestClient_ListDirectChildren_BadKind(t *testing.T) {
	c := NewClient("http://127.0.0.1:0", "test@example.com", "api-token")
	if _, _, err := c.ListDirectChildren(context.Background(), ContentKind("whiteboard"), "1", "", 25); err == nil {
		t.Fatal("expected error for unsupported kind")
	}
}

func TestClient_GetFolderAncestors(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`{"results": [{"id": "1", "type": "page"}, {"id": "5", "type": "folder"}], "_links": {}}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	anc, err := c.GetFolderAncestors(context.Background(), "777")
	if err != nil {
		t.Fatalf("GetFolderAncestors() error: %v", err)
	}
	if gotPath != "/wiki/api/v2/folders/777/ancestors" {
		t.Errorf("path = %q", gotPath)
	}
	if len(anc) != 2 || anc[0].ID != "1" || anc[1].Type != "folder" {
		t.Errorf("ancestors = %+v", anc)
	}
}

func TestClient_GetFolderAncestors_Paginated(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "C2" {
			w.Write([]byte(`{"results": [{"id": "3", "type": "page"}], "_links": {}}`))
			return
		}
		w.Write([]byte(`{"results": [{"id": "1", "type": "page"}, {"id": "2", "type": "folder"}], "_links": {"next": "` + srv.URL + `/wiki/api/v2/folders/777/ancestors?cursor=C2"}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test@example.com", "api-token")
	anc, err := c.GetFolderAncestors(context.Background(), "777")
	if err != nil {
		t.Fatalf("GetFolderAncestors() error: %v", err)
	}
	if len(anc) != 3 || anc[2].ID != "3" {
		t.Errorf("ancestors = %+v, want 3 drained across pages", anc)
	}
}

func TestClient_GetFolderAncestors_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	if _, err := c.GetFolderAncestors(context.Background(), "777"); !errors.Is(err, ErrFolderNotFound) {
		t.Fatalf("expected ErrFolderNotFound, got %v", err)
	}
}

func TestClient_CreateFolder(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(folderJSON))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	f, err := c.CreateFolder(context.Background(), CreateFolderParams{SpaceID: "space1", Title: "Pillars", ParentID: "100"})
	if err != nil {
		t.Fatalf("CreateFolder() error: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/wiki/api/v2/folders" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	if gotBody["spaceId"] != "space1" || gotBody["title"] != "Pillars" || gotBody["parentId"] != "100" {
		t.Errorf("body = %v", gotBody)
	}
	if f.ID != "777" || f.CreatedAt != "2026-09-28T20:14:30.759Z" {
		t.Errorf("folder = %+v", f)
	}
}

func TestClient_CreateFolder_NoParentOmitsField(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(folderJSON))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	if _, err := c.CreateFolder(context.Background(), CreateFolderParams{SpaceID: "space1", Title: "Pillars"}); err != nil {
		t.Fatalf("CreateFolder() error: %v", err)
	}
	if _, ok := gotBody["parentId"]; ok {
		t.Errorf("parentId should be omitted, got %v", gotBody["parentId"])
	}
}

func TestClient_DeleteFolder(t *testing.T) {
	var gotMethod, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	if err := c.DeleteFolder(context.Background(), "777"); err != nil {
		t.Fatalf("DeleteFolder() error: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/wiki/api/v2/folders/777" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
}

func TestClient_DeleteFolder_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	if err := c.DeleteFolder(context.Background(), "777"); !errors.Is(err, ErrFolderNotFound) {
		t.Fatalf("expected ErrFolderNotFound, got %v", err)
	}
}
