package confluence

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tammersaleh/confluence-sync/internal/httpx"
	"github.com/tammersaleh/confluence-sync/internal/output"
)

func TestClient_GetSpace(t *testing.T) {
	tests := []struct {
		name       string
		spaceKey   string
		response   string
		statusCode int
		wantSpace  *Space
		wantErr    error
	}{
		{
			name:     "success",
			spaceKey: "ENG",
			response: `{
				"results": [{
					"id": "12345",
					"key": "ENG",
					"name": "Engineering",
					"type": "global",
					"status": "current",
					"homepageId": "999"
				}]
			}`,
			statusCode: http.StatusOK,
			wantSpace: &Space{
				ID:         "12345",
				Key:        "ENG",
				Name:       "Engineering",
				Type:       "global",
				Status:     "current",
				HomepageID: "999",
			},
		},
		{
			name:       "not found",
			spaceKey:   "NOTFOUND",
			response:   `{"results": []}`,
			statusCode: http.StatusOK,
			wantErr:    ErrSpaceNotFound,
		},
		{
			name:       "unauthorized",
			spaceKey:   "ENG",
			response:   `{"message": "Unauthorized"}`,
			statusCode: http.StatusUnauthorized,
			wantErr:    ErrUnauthorized,
		},
		{
			name:       "forbidden",
			spaceKey:   "ENG",
			response:   `{"message": "Forbidden"}`,
			statusCode: http.StatusForbidden,
			wantErr:    ErrForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/wiki/api/v2/spaces" {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				if r.URL.Query().Get("keys") != tt.spaceKey {
					t.Errorf("expected keys=%s, got %s", tt.spaceKey, r.URL.Query().Get("keys"))
				}
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.response))
			}))
			defer server.Close()

			c := NewClient(server.URL, "test@example.com", "api-token")
			space, err := c.GetSpace(context.Background(), tt.spaceKey)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("GetSpace() error = %v, wantErr %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Errorf("GetSpace() unexpected error: %v", err)
				return
			}
			if space.ID != tt.wantSpace.ID || space.Key != tt.wantSpace.Key || space.Name != tt.wantSpace.Name ||
				space.Type != tt.wantSpace.Type || space.Status != tt.wantSpace.Status || space.HomepageID != tt.wantSpace.HomepageID {
				t.Errorf("GetSpace() = %+v, want %+v", space, tt.wantSpace)
			}
		})
	}
}

func TestClient_GetCurrentUser(t *testing.T) {
	tests := []struct {
		name            string
		response        string
		statusCode      int
		wantAccountID   string
		wantDisplayName string
		wantEmail       string
		wantErr         error
	}{
		{
			name:            "success",
			response:        `{"accountId":"5b10","displayName":"Ada","email":"ada@example.com"}`,
			statusCode:      http.StatusOK,
			wantAccountID:   "5b10",
			wantDisplayName: "Ada",
			wantEmail:       "ada@example.com",
		},
		{
			name:            "publicName fallback",
			response:        `{"accountId":"5b10","displayName":"","publicName":"Ada L"}`,
			statusCode:      http.StatusOK,
			wantAccountID:   "5b10",
			wantDisplayName: "Ada L",
		},
		{
			name:       "unauthorized",
			response:   `{"message": "Unauthorized"}`,
			statusCode: http.StatusUnauthorized,
			wantErr:    ErrUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/wiki/rest/api/user/current" {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.response))
			}))
			defer server.Close()

			c := NewClient(server.URL, "test@example.com", "api-token")
			user, err := c.GetCurrentUser(context.Background())

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("GetCurrentUser() error = %v, wantErr %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Errorf("GetCurrentUser() unexpected error: %v", err)
				return
			}
			if user.AccountID != tt.wantAccountID {
				t.Errorf("AccountID = %q, want %q", user.AccountID, tt.wantAccountID)
			}
			if user.DisplayName != tt.wantDisplayName {
				t.Errorf("DisplayName = %q, want %q", user.DisplayName, tt.wantDisplayName)
			}
			if user.Email != tt.wantEmail {
				t.Errorf("Email = %q, want %q", user.Email, tt.wantEmail)
			}
		})
	}
}

func TestClient_GetPages(t *testing.T) {
	tests := []struct {
		name      string
		spaceID   string
		wantPages []Page
	}{
		{
			name:    "fetches parentId for each page",
			spaceID: "12345",
			wantPages: []Page{
				{ID: "1", Title: "Page One", ParentID: ""},
				{ID: "2", Title: "Page Two", ParentID: "1"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/wiki/api/v2/spaces/12345/pages":
					// List pages endpoint - returns IDs and titles only
					w.Write([]byte(`{
						"results": [
							{"id": "1", "title": "Page One"},
							{"id": "2", "title": "Page Two"}
						]
					}`))
				case "/wiki/api/v2/pages/1":
					// Individual page endpoint - returns parentId
					w.Write([]byte(`{"id": "1", "parentId": ""}`))
				case "/wiki/api/v2/pages/2":
					w.Write([]byte(`{"id": "2", "parentId": "1"}`))
				default:
					t.Errorf("unexpected path: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()

			c := NewClient(server.URL, "test@example.com", "api-token")
			pages, err := c.GetPages(context.Background(), tt.spaceID)

			if err != nil {
				t.Errorf("GetPages() unexpected error: %v", err)
				return
			}
			if len(pages) != len(tt.wantPages) {
				t.Errorf("GetPages() got %d pages, want %d", len(pages), len(tt.wantPages))
				return
			}
			for i, p := range pages {
				if p.ID != tt.wantPages[i].ID || p.Title != tt.wantPages[i].Title || p.ParentID != tt.wantPages[i].ParentID {
					t.Errorf("GetPages()[%d] = %+v, want %+v", i, p, tt.wantPages[i])
				}
			}
		})
	}
}

func TestClient_GetPages_Paginated(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/wiki/api/v2/spaces/12345/pages" && callCount == 0:
			callCount++
			w.Write([]byte(`{
				"results": [{"id": "1", "title": "Page One"}],
				"_links": {"next": "/wiki/api/v2/spaces/12345/pages?cursor=abc"}
			}`))
		case r.URL.Path == "/wiki/api/v2/spaces/12345/pages" && callCount == 1:
			callCount++
			w.Write([]byte(`{
				"results": [{"id": "2", "title": "Page Two"}]
			}`))
		case r.URL.Path == "/wiki/api/v2/pages/1":
			w.Write([]byte(`{"id": "1", "parentId": ""}`))
		case r.URL.Path == "/wiki/api/v2/pages/2":
			w.Write([]byte(`{"id": "2", "parentId": "1"}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	pages, err := c.GetPages(context.Background(), "12345")

	if err != nil {
		t.Fatalf("GetPages() unexpected error: %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("got %d pages, want 2", len(pages))
	}
	if pages[0].ParentID != "" {
		t.Errorf("pages[0].ParentID = %q, want empty", pages[0].ParentID)
	}
	if pages[1].ParentID != "1" {
		t.Errorf("pages[1].ParentID = %q, want \"1\"", pages[1].ParentID)
	}
}

func TestClient_ListPages(t *testing.T) {
	var reqCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount++
		if r.URL.Path != "/wiki/api/v2/spaces/12345/pages" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Write([]byte(`{
			"results": [
				{"id": "1", "title": "Page One"},
				{"id": "2", "title": "Page Two", "parentId": "1", "parentType": "page"}
			],
			"_links": {"next": "/wiki/api/v2/spaces/12345/pages?limit=25&cursor=NEXT"}
		}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	pages, nextCursor, err := c.ListPages(context.Background(), "12345", "", 25)
	if err != nil {
		t.Fatalf("ListPages() error: %v", err)
	}
	if reqCount != 1 {
		t.Errorf("expected exactly 1 HTTP request (no N+1), got %d", reqCount)
	}
	if len(pages) != 2 {
		t.Fatalf("got %d pages, want 2", len(pages))
	}
	if pages[0].ID != "1" || pages[0].Title != "Page One" {
		t.Errorf("pages[0] = %+v, want ID=1 Title=Page One", pages[0])
	}
	if pages[0].Type != "page" {
		t.Errorf("pages[0].Type = %q, want page", pages[0].Type)
	}
	if pages[1].ParentID != "1" {
		t.Errorf("pages[1].ParentID = %q, want 1", pages[1].ParentID)
	}
	if pages[1].ParentType != "page" {
		t.Errorf("pages[1].ParentType = %q, want page", pages[1].ParentType)
	}
	if nextCursor != "NEXT" {
		t.Errorf("nextCursor = %q, want NEXT", nextCursor)
	}
}

func TestClient_ListPages_LastPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"results": [{"id": "1", "title": "Only Page"}],
			"_links": {"next": ""}
		}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	pages, nextCursor, err := c.ListPages(context.Background(), "12345", "", 25)
	if err != nil {
		t.Fatalf("ListPages() error: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("got %d pages, want 1", len(pages))
	}
	if nextCursor != "" {
		t.Errorf("nextCursor = %q, want empty", nextCursor)
	}
}

func TestClient_ListPages_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	_, _, err := c.ListPages(context.Background(), "12345", "", 25)
	if !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("expected ErrSpaceNotFound, got %v", err)
	}
}

func TestClient_ListPages_DefaultLimit(t *testing.T) {
	var gotLimit string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLimit = r.URL.Query().Get("limit")
		w.Write([]byte(`{"results": []}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	_, _, err := c.ListPages(context.Background(), "12345", "", 0)
	if err != nil {
		t.Fatalf("ListPages() error: %v", err)
	}
	if gotLimit != "25" {
		t.Errorf("limit = %q, want 25", gotLimit)
	}
}

func TestClient_ListChildren(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/api/v2/pages/100/children" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Write([]byte(`{
			"results": [
				{"id": "1", "title": "Child One", "status": "current", "spaceId": "s1", "childPosition": 0},
				{"id": "2", "title": "Child Two", "status": "current", "spaceId": "s1", "childPosition": 1}
			],
			"_links": {"next": "/wiki/api/v2/pages/100/children?limit=25&cursor=NEXT"}
		}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	pages, nextCursor, err := c.ListChildren(context.Background(), "100", "", 25)
	if err != nil {
		t.Fatalf("ListChildren() error: %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("got %d pages, want 2", len(pages))
	}
	if pages[0].ID != "1" || pages[0].Title != "Child One" {
		t.Errorf("pages[0] = %+v, want ID=1 Title=Child One", pages[0])
	}
	if pages[0].Type != "page" {
		t.Errorf("pages[0].Type = %q, want page", pages[0].Type)
	}
	if pages[0].ParentID != "" {
		t.Errorf("pages[0].ParentID = %q, want empty", pages[0].ParentID)
	}
	if nextCursor != "NEXT" {
		t.Errorf("nextCursor = %q, want NEXT", nextCursor)
	}
}

func TestClient_ListChildren_DefaultLimit(t *testing.T) {
	var gotLimit string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLimit = r.URL.Query().Get("limit")
		w.Write([]byte(`{"results": []}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	_, _, err := c.ListChildren(context.Background(), "100", "", 0)
	if err != nil {
		t.Fatalf("ListChildren() error: %v", err)
	}
	if gotLimit != "25" {
		t.Errorf("limit = %q, want 25", gotLimit)
	}
}

func TestClient_ListChildren_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	_, _, err := c.ListChildren(context.Background(), "100", "", 25)
	if !errors.Is(err, ErrPageNotFound) {
		t.Fatalf("expected ErrPageNotFound, got %v", err)
	}
}

func TestClient_GetAncestors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/api/v2/pages/100/ancestors" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Write([]byte(`{
			"results": [
				{"id": "1", "type": "page"},
				{"id": "2", "type": "page"}
			]
		}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	ancestors, err := c.GetAncestors(context.Background(), "100")
	if err != nil {
		t.Fatalf("GetAncestors() error: %v", err)
	}
	if len(ancestors) != 2 {
		t.Fatalf("got %d ancestors, want 2", len(ancestors))
	}
	if ancestors[0].ID != "1" || ancestors[0].Type != "page" {
		t.Errorf("ancestors[0] = %+v, want ID=1 Type=page", ancestors[0])
	}
	if ancestors[1].ID != "2" {
		t.Errorf("ancestors[1].ID = %q, want 2", ancestors[1].ID)
	}
}

func TestClient_GetAncestors_Paginated(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/api/v2/pages/100/ancestors" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		callCount++
		if callCount == 1 {
			w.Write([]byte(`{
				"results": [{"id": "1", "type": "page"}],
				"_links": {"next": "/wiki/api/v2/pages/100/ancestors?cursor=abc"}
			}`))
		} else {
			w.Write([]byte(`{
				"results": [{"id": "2", "type": "folder"}]
			}`))
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	ancestors, err := c.GetAncestors(context.Background(), "100")
	if err != nil {
		t.Fatalf("GetAncestors() error: %v", err)
	}
	if callCount != 2 {
		t.Errorf("expected 2 API calls, got %d", callCount)
	}
	if len(ancestors) != 2 {
		t.Fatalf("got %d ancestors, want 2", len(ancestors))
	}
	if ancestors[0].ID != "1" || ancestors[1].ID != "2" {
		t.Errorf("ancestors = %+v, want IDs 1 then 2", ancestors)
	}
	if ancestors[1].Type != "folder" {
		t.Errorf("ancestors[1].Type = %q, want folder", ancestors[1].Type)
	}
}

func TestClient_GetAncestors_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	_, err := c.GetAncestors(context.Background(), "100")
	if !errors.Is(err, ErrPageNotFound) {
		t.Fatalf("expected ErrPageNotFound, got %v", err)
	}
}

func TestClient_ListSpaces(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/api/v2/spaces" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("keys") != "" {
			t.Errorf("ListSpaces must not send a keys param, got %q", r.URL.Query().Get("keys"))
		}
		w.Write([]byte(`{
			"results": [
				{"id": "1", "key": "ENG", "name": "Engineering", "type": "global", "status": "current", "homepageId": "999"},
				{"id": "2", "key": "OPS", "name": "Operations", "type": "global", "status": "current", "homepageId": "888"}
			],
			"_links": {"next": "/wiki/api/v2/spaces?limit=25&cursor=NEXT"}
		}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	spaces, nextCursor, err := c.ListSpaces(context.Background(), "", 25)
	if err != nil {
		t.Fatalf("ListSpaces() error: %v", err)
	}
	if len(spaces) != 2 {
		t.Fatalf("got %d spaces, want 2", len(spaces))
	}
	if spaces[0].ID != "1" || spaces[0].Key != "ENG" || spaces[0].Name != "Engineering" {
		t.Errorf("spaces[0] = %+v, want ID=1 Key=ENG Name=Engineering", spaces[0])
	}
	if spaces[0].Type != "global" {
		t.Errorf("spaces[0].Type = %q, want global", spaces[0].Type)
	}
	if spaces[0].Status != "current" {
		t.Errorf("spaces[0].Status = %q, want current", spaces[0].Status)
	}
	if spaces[0].HomepageID != "999" {
		t.Errorf("spaces[0].HomepageID = %q, want 999", spaces[0].HomepageID)
	}
	if spaces[1].Key != "OPS" {
		t.Errorf("spaces[1].Key = %q, want OPS", spaces[1].Key)
	}
	if nextCursor != "NEXT" {
		t.Errorf("nextCursor = %q, want NEXT", nextCursor)
	}
}

func TestClient_ListSpaces_DefaultLimit(t *testing.T) {
	var gotLimit string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLimit = r.URL.Query().Get("limit")
		w.Write([]byte(`{"results": []}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	_, _, err := c.ListSpaces(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("ListSpaces() error: %v", err)
	}
	if gotLimit != "25" {
		t.Errorf("limit = %q, want 25", gotLimit)
	}
}

func TestClient_GetPage_Storage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/api/v2/pages/123" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("body-format"); got != "storage" {
			t.Errorf("body-format = %q, want storage", got)
		}
		w.Write([]byte(`{
			"id": "123",
			"title": "My Page",
			"spaceId": "space1",
			"authorId": "user456",
			"createdAt": "2024-01-15T10:30:00.000Z",
			"body": {"storage": {"value": "<p>Hello</p>"}},
			"version": {"number": 5, "createdAt": "2024-06-20T14:45:00.000Z"},
			"_links": {"webui": "/spaces/TEST/pages/123/My+Page"}
		}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	page, err := c.GetPage(context.Background(), "123", BodyFormatStorage)
	if err != nil {
		t.Fatalf("GetPage() error: %v", err)
	}
	if page.Body != "<p>Hello</p>" {
		t.Errorf("Body = %q, want <p>Hello</p>", page.Body)
	}
	if page.BodyFormat != BodyFormatStorage {
		t.Errorf("BodyFormat = %q, want storage", page.BodyFormat)
	}
	if page.Version != 5 {
		t.Errorf("Version = %d, want 5", page.Version)
	}
	if page.AuthorID != "user456" {
		t.Errorf("AuthorID = %q, want user456", page.AuthorID)
	}
	if page.SpaceID != "space1" {
		t.Errorf("SpaceID = %q, want space1", page.SpaceID)
	}
	if page.ModifiedAt != "2024-06-20T14:45:00.000Z" {
		t.Errorf("ModifiedAt = %q, want 2024-06-20T14:45:00.000Z", page.ModifiedAt)
	}
	wantURL := server.URL + "/wiki/spaces/TEST/pages/123/My+Page"
	if page.WebURL != wantURL {
		t.Errorf("WebURL = %q, want %q", page.WebURL, wantURL)
	}
}

func TestClient_GetPage_AtlasDoc(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("body-format"); got != "atlas_doc_format" {
			t.Errorf("body-format = %q, want atlas_doc_format", got)
		}
		w.Write([]byte(`{
			"id": "123",
			"title": "My Page",
			"body": {"atlas_doc_format": {"value": "{\"type\":\"doc\"}"}},
			"version": {"number": 1, "createdAt": "2024-06-20T14:45:00.000Z"},
			"_links": {"webui": "/spaces/TEST/pages/123"}
		}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	page, err := c.GetPage(context.Background(), "123", BodyFormatAtlasDoc)
	if err != nil {
		t.Fatalf("GetPage() error: %v", err)
	}
	if page.Body != `{"type":"doc"}` {
		t.Errorf("Body = %q, want ADF JSON string", page.Body)
	}
	if page.BodyFormat != BodyFormatAtlasDoc {
		t.Errorf("BodyFormat = %q, want atlas_doc_format", page.BodyFormat)
	}
}

func TestClient_GetPage_View(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("body-format"); got != "view" {
			t.Errorf("body-format = %q, want view", got)
		}
		w.Write([]byte(`{
			"id": "123",
			"title": "My Page",
			"body": {"view": {"value": "<div>rendered</div>"}},
			"version": {"number": 1, "createdAt": "2024-06-20T14:45:00.000Z"},
			"_links": {"webui": "/spaces/TEST/pages/123"}
		}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	page, err := c.GetPage(context.Background(), "123", BodyFormatView)
	if err != nil {
		t.Fatalf("GetPage() error: %v", err)
	}
	if page.Body != "<div>rendered</div>" {
		t.Errorf("Body = %q, want <div>rendered</div>", page.Body)
	}
}

func TestClient_GetPage_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	_, err := c.GetPage(context.Background(), "123", BodyFormatStorage)
	if !errors.Is(err, ErrPageNotFound) {
		t.Fatalf("expected ErrPageNotFound, got %v", err)
	}
}

func TestClient_GetPage_UnsupportedFormat(t *testing.T) {
	c := NewClient("http://example.com", "test@example.com", "api-token")
	_, err := c.GetPage(context.Background(), "123", APIBodyFormat("bogus"))
	if err == nil {
		t.Fatal("expected error for unsupported body format, got nil")
	}
}

func TestClient_GetPageContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/api/v2/pages/123" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Write([]byte(`{
			"id": "123",
			"title": "My Page",
			"authorId": "user456",
			"createdAt": "2024-01-15T10:30:00.000Z",
			"body": {"storage": {"value": "<p>Hello</p>"}},
			"version": {"number": 5, "createdAt": "2024-06-20T14:45:00.000Z", "authorId": "user789"},
			"_links": {"webui": "/wiki/spaces/TEST/pages/123/My+Page"}
		}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	content, err := c.GetPageContent(context.Background(), "123")
	if err != nil {
		t.Fatalf("GetPageContent() error: %v", err)
	}
	if content.ID != "123" {
		t.Errorf("ID = %s, want 123", content.ID)
	}
	if content.Title != "My Page" {
		t.Errorf("Title = %s, want My Page", content.Title)
	}
	if content.Body != "<p>Hello</p>" {
		t.Errorf("Body = %s, want <p>Hello</p>", content.Body)
	}
	if content.Version != 5 {
		t.Errorf("Version = %d, want 5", content.Version)
	}
	if content.AuthorID != "user456" {
		t.Errorf("AuthorID = %s, want user456", content.AuthorID)
	}
	if content.CreatedAt != "2024-01-15T10:30:00.000Z" {
		t.Errorf("CreatedAt = %s, want 2024-01-15T10:30:00.000Z", content.CreatedAt)
	}
	if content.ModifiedAt != "2024-06-20T14:45:00.000Z" {
		t.Errorf("ModifiedAt = %s, want 2024-06-20T14:45:00.000Z", content.ModifiedAt)
	}
	wantURL := server.URL + "/wiki/spaces/TEST/pages/123/My+Page"
	if content.WebURL != wantURL {
		t.Errorf("WebURL = %s, want %s", content.WebURL, wantURL)
	}
}

func TestClient_GetPageContent_WebUIWithoutWikiPrefix(t *testing.T) {
	// The Confluence v2 API returns _links.webui without the /wiki prefix
	// (e.g. "/spaces/SA/pages/123/My+Page" instead of "/wiki/spaces/SA/pages/123/My+Page").
	// The constructed URL must still include /wiki to be a valid browser URL.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"id": "123",
			"title": "My Page",
			"authorId": "user456",
			"createdAt": "2024-01-15T10:30:00.000Z",
			"body": {"storage": {"value": "<p>Hello</p>"}},
			"version": {"number": 5, "createdAt": "2024-06-20T14:45:00.000Z", "authorId": "user789"},
			"_links": {"webui": "/spaces/TEST/pages/123/My+Page"}
		}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	content, err := c.GetPageContent(context.Background(), "123")
	if err != nil {
		t.Fatalf("GetPageContent() error: %v", err)
	}

	wantURL := server.URL + "/wiki/spaces/TEST/pages/123/My+Page"
	if content.WebURL != wantURL {
		t.Errorf("WebURL = %s, want %s", content.WebURL, wantURL)
	}
}

func TestClient_GetAttachments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/api/v2/pages/123/attachments" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Write([]byte(`{
			"results": [
				{
					"id": "att1",
					"title": "diagram.png",
					"mediaType": "image/png",
					"downloadLink": "/wiki/download/attachments/123/diagram.png"
				},
				{
					"id": "att2",
					"title": "doc.pdf",
					"mediaType": "application/pdf",
					"downloadLink": "/wiki/download/attachments/123/doc.pdf"
				}
			]
		}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	attachments, err := c.GetAttachments(context.Background(), "123")
	if err != nil {
		t.Fatalf("GetAttachments() error: %v", err)
	}
	if len(attachments) != 2 {
		t.Fatalf("got %d attachments, want 2", len(attachments))
	}
	if attachments[0].Title != "diagram.png" {
		t.Errorf("attachments[0].Title = %s, want diagram.png", attachments[0].Title)
	}
	if attachments[1].MediaType != "application/pdf" {
		t.Errorf("attachments[1].MediaType = %s, want application/pdf", attachments[1].MediaType)
	}
}

func TestClient_GetAttachments_Paginated(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.Write([]byte(`{
				"results": [
					{"id": "att1", "title": "file1.png", "mediaType": "image/png", "downloadLink": "/download/1"}
				],
				"_links": {"next": "/wiki/api/v2/pages/123/attachments?cursor=abc"}
			}`))
		} else {
			w.Write([]byte(`{
				"results": [
					{"id": "att2", "title": "file2.png", "mediaType": "image/png", "downloadLink": "/download/2"}
				]
			}`))
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	attachments, err := c.GetAttachments(context.Background(), "123")
	if err != nil {
		t.Fatalf("GetAttachments() error: %v", err)
	}

	if callCount != 2 {
		t.Errorf("expected 2 API calls for pagination, got %d", callCount)
	}
	if len(attachments) != 2 {
		t.Fatalf("got %d attachments, want 2", len(attachments))
	}
	if attachments[0].Title != "file1.png" {
		t.Errorf("attachments[0].Title = %s, want file1.png", attachments[0].Title)
	}
	if attachments[1].Title != "file2.png" {
		t.Errorf("attachments[1].Title = %s, want file2.png", attachments[1].Title)
	}
}

func TestClient_DownloadAttachment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/download/attachments/123/diagram.png" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Write([]byte("fake image data"))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	att := Attachment{
		ID:          "att1",
		Title:       "diagram.png",
		DownloadURL: "/wiki/download/attachments/123/diagram.png",
	}
	rc, err := c.DownloadAttachment(context.Background(), att)
	if err != nil {
		t.Fatalf("DownloadAttachment() error: %v", err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}
	if string(data) != "fake image data" {
		t.Errorf("got %q, want %q", string(data), "fake image data")
	}
}

func TestClient_GetContentParent_Database(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/api/v2/databases/db1" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(`{
			"id": "db1",
			"title": "Customers",
			"parentId": "page1",
			"parentType": "page"
		}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	page, err := c.GetContentParent(context.Background(), "db1", "database")
	if err != nil {
		t.Fatalf("GetContentParent() error: %v", err)
	}
	if page.ID != "db1" {
		t.Errorf("ID = %s, want db1", page.ID)
	}
	if page.Title != "Customers" {
		t.Errorf("Title = %s, want Customers", page.Title)
	}
	if page.ParentID != "page1" {
		t.Errorf("ParentID = %s, want page1", page.ParentID)
	}
	if page.ParentType != "page" {
		t.Errorf("ParentType = %s, want page", page.ParentType)
	}
	if page.Type != "database" {
		t.Errorf("Type = %s, want database", page.Type)
	}
}

func TestClient_GetContentParent_Folder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/api/v2/folders/f1" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(`{
			"id": "f1",
			"title": "Archive",
			"parentId": "",
			"parentType": ""
		}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	page, err := c.GetContentParent(context.Background(), "f1", "folder")
	if err != nil {
		t.Fatalf("GetContentParent() error: %v", err)
	}
	if page.ID != "f1" {
		t.Errorf("ID = %s, want f1", page.ID)
	}
	if page.Title != "Archive" {
		t.Errorf("Title = %s, want Archive", page.Title)
	}
	if page.Type != "folder" {
		t.Errorf("Type = %s, want folder", page.Type)
	}
}

func TestClient_GetPages_ResolvesNonPageParents(t *testing.T) {
	// Page "3" has parentType "database", pointing to database "db1".
	// Database "db1" has parentType "page", pointing to page "1".
	// GetPages should resolve db1 and include it in the returned pages.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wiki/api/v2/spaces/12345/pages":
			w.Write([]byte(`{
				"results": [
					{"id": "1", "title": "Root"},
					{"id": "2", "title": "Normal Child"},
					{"id": "3", "title": "Jane Street"}
				]
			}`))
		case "/wiki/api/v2/pages/1":
			w.Write([]byte(`{"id": "1", "parentId": "", "parentType": ""}`))
		case "/wiki/api/v2/pages/2":
			w.Write([]byte(`{"id": "2", "parentId": "1", "parentType": "page"}`))
		case "/wiki/api/v2/pages/3":
			w.Write([]byte(`{"id": "3", "parentId": "db1", "parentType": "database"}`))
		case "/wiki/api/v2/databases/db1":
			w.Write([]byte(`{
				"id": "db1",
				"title": "Customers",
				"parentId": "1",
				"parentType": "page"
			}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	pages, err := c.GetPages(context.Background(), "12345")
	if err != nil {
		t.Fatalf("GetPages() error: %v", err)
	}

	// Should have 4 items: 3 pages + 1 synthetic database node
	if len(pages) != 4 {
		t.Fatalf("got %d pages, want 4", len(pages))
	}

	// Find the database node
	var dbNode *Page
	for i := range pages {
		if pages[i].ID == "db1" {
			dbNode = &pages[i]
			break
		}
	}
	if dbNode == nil {
		t.Fatal("expected database node db1 in pages")
	}
	if dbNode.Title != "Customers" {
		t.Errorf("dbNode.Title = %s, want Customers", dbNode.Title)
	}
	if dbNode.Type != "database" {
		t.Errorf("dbNode.Type = %s, want database", dbNode.Type)
	}
	if dbNode.ParentID != "1" {
		t.Errorf("dbNode.ParentID = %s, want 1", dbNode.ParentID)
	}

	// Jane Street should have parentID=db1 and parentType=database
	var janeStreet *Page
	for i := range pages {
		if pages[i].ID == "3" {
			janeStreet = &pages[i]
			break
		}
	}
	if janeStreet == nil {
		t.Fatal("expected Jane Street page in pages")
	}
	if janeStreet.ParentID != "db1" {
		t.Errorf("janeStreet.ParentID = %s, want db1", janeStreet.ParentID)
	}
	if janeStreet.ParentType != "database" {
		t.Errorf("janeStreet.ParentType = %s, want database", janeStreet.ParentType)
	}
}

func TestClient_GetPages_ResolvesChainedNonPageParents(t *testing.T) {
	// Page "2" is under database "db1", which is under folder "f1", which is under page "1".
	// GetPages should resolve both db1 and f1.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wiki/api/v2/spaces/12345/pages":
			w.Write([]byte(`{
				"results": [
					{"id": "1", "title": "Root"},
					{"id": "2", "title": "Deep Page"}
				]
			}`))
		case "/wiki/api/v2/pages/1":
			w.Write([]byte(`{"id": "1", "parentId": "", "parentType": ""}`))
		case "/wiki/api/v2/pages/2":
			w.Write([]byte(`{"id": "2", "parentId": "db1", "parentType": "database"}`))
		case "/wiki/api/v2/databases/db1":
			w.Write([]byte(`{
				"id": "db1",
				"title": "Customers",
				"parentId": "f1",
				"parentType": "folder"
			}`))
		case "/wiki/api/v2/folders/f1":
			w.Write([]byte(`{
				"id": "f1",
				"title": "Archive",
				"parentId": "1",
				"parentType": "page"
			}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	pages, err := c.GetPages(context.Background(), "12345")
	if err != nil {
		t.Fatalf("GetPages() error: %v", err)
	}

	// Should have 4: 2 pages + database + folder
	if len(pages) != 4 {
		t.Fatalf("got %d pages, want 4", len(pages))
	}

	// Verify both synthetic nodes exist
	found := map[string]bool{}
	for _, p := range pages {
		found[p.ID] = true
	}
	if !found["db1"] {
		t.Error("expected db1 in pages")
	}
	if !found["f1"] {
		t.Error("expected f1 in pages")
	}
}

func TestClient_StatusError_Unauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	_, err := c.GetSpace(context.Background(), "ENG")

	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("expected *StatusError, got %T", err)
	}
	if se.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want 401", se.StatusCode)
	}
	if se.Endpoint != "/wiki/api/v2/spaces" {
		t.Errorf("Endpoint = %q, want /wiki/api/v2/spaces", se.Endpoint)
	}
}

func TestClient_StatusError_Forbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	_, err := c.GetSpace(context.Background(), "ENG")

	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("expected *StatusError, got %T", err)
	}
	if se.StatusCode != http.StatusForbidden {
		t.Errorf("StatusCode = %d, want 403", se.StatusCode)
	}
}

func TestClient_GetSpace_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	_, err := c.GetSpace(context.Background(), "ENG")

	if !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("expected ErrSpaceNotFound, got %v", err)
	}
}

func TestClient_GetSpaceByID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/api/v2/spaces/12345" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Write([]byte(`{
			"id": "12345",
			"key": "ENG",
			"name": "Engineering",
			"type": "global",
			"status": "current",
			"homepageId": "999"
		}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	space, err := c.GetSpaceByID(context.Background(), "12345")
	if err != nil {
		t.Fatalf("GetSpaceByID() error: %v", err)
	}
	if space.ID != "12345" {
		t.Errorf("ID = %q, want 12345", space.ID)
	}
	if space.Key != "ENG" {
		t.Errorf("Key = %q, want ENG", space.Key)
	}
	if space.Name != "Engineering" {
		t.Errorf("Name = %q, want Engineering", space.Name)
	}
	if space.Type != "global" {
		t.Errorf("Type = %q, want global", space.Type)
	}
	if space.Status != "current" {
		t.Errorf("Status = %q, want current", space.Status)
	}
	if space.HomepageID != "999" {
		t.Errorf("HomepageID = %q, want 999", space.HomepageID)
	}
}

func TestClient_GetSpaceByID_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	_, err := c.GetSpaceByID(context.Background(), "12345")
	if !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("expected ErrSpaceNotFound, got %v", err)
	}
}

func TestClient_GetAttachmentByID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/api/v2/attachments/att1" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Write([]byte(`{
			"id": "att1",
			"title": "diagram.png",
			"mediaType": "image/png",
			"downloadLink": "/wiki/download/attachments/123/diagram.png"
		}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	att, err := c.GetAttachmentByID(context.Background(), "att1")
	if err != nil {
		t.Fatalf("GetAttachmentByID() error: %v", err)
	}
	if att.ID != "att1" {
		t.Errorf("ID = %q, want att1", att.ID)
	}
	if att.Title != "diagram.png" {
		t.Errorf("Title = %q, want diagram.png", att.Title)
	}
	if att.MediaType != "image/png" {
		t.Errorf("MediaType = %q, want image/png", att.MediaType)
	}
	if att.DownloadURL != "/wiki/download/attachments/123/diagram.png" {
		t.Errorf("DownloadURL = %q, want /wiki/download/attachments/123/diagram.png", att.DownloadURL)
	}
}

func TestClient_GetAttachmentByID_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	_, err := c.GetAttachmentByID(context.Background(), "att1")
	if !errors.Is(err, ErrAttachmentNotFound) {
		t.Fatalf("expected ErrAttachmentNotFound, got %v", err)
	}
}

func TestClient_GetAttachments_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	_, err := c.GetAttachments(context.Background(), "123")

	if !errors.Is(err, ErrPageNotFound) {
		t.Fatalf("expected ErrPageNotFound, got %v", err)
	}
}

func TestClient_RateLimitExhaustion(t *testing.T) {
	old := baseRetryDelay
	baseRetryDelay = time.Millisecond
	defer func() { baseRetryDelay = old }()

	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	_, err := c.GetSpace(context.Background(), "ENG")

	var rl *httpx.RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("expected *httpx.RateLimitError, got %T: %v", err, err)
	}
	if rl.Endpoint != "/wiki/api/v2/spaces" {
		t.Errorf("Endpoint = %q, want /wiki/api/v2/spaces", rl.Endpoint)
	}
	if rl.Retries != maxRetries {
		t.Errorf("Retries = %d, want %d", rl.Retries, maxRetries)
	}
	if calls != maxRetries+1 {
		t.Errorf("server calls = %d, want %d", calls, maxRetries+1)
	}
}

func TestClient_ServerErrorExhaustion(t *testing.T) {
	// 5xx exhaustion must NOT surface as a rate-limit error. It wraps the last
	// *StatusError (500 / ErrAPIError) via the "after N retries: %w" path.
	old := baseRetryDelay
	baseRetryDelay = time.Millisecond
	defer func() { baseRetryDelay = old }()

	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	_, err := c.GetSpace(context.Background(), "ENG")

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var rl *httpx.RateLimitError
	if errors.As(err, &rl) {
		t.Fatalf("5xx exhaustion must not be a *httpx.RateLimitError, got %v", err)
	}
	if !errors.Is(err, ErrAPIError) {
		t.Errorf("expected error to wrap ErrAPIError, got %v", err)
	}
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("expected wrapped *StatusError, got %T: %v", err, err)
	}
	if se.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want 500", se.StatusCode)
	}
	if calls != maxRetries+1 {
		t.Errorf("server calls = %d, want %d", calls, maxRetries+1)
	}
}

func TestClient_NetworkErrorExhaustion(t *testing.T) {
	// A dead server (closed before the call) makes every attempt fail at the
	// transport layer. The exhausted error wraps a *url.Error, which
	// ClassifyError must map to ExitNetwork.
	old := baseRetryDelay
	baseRetryDelay = time.Millisecond
	defer func() { baseRetryDelay = old }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := server.URL
	server.Close() // nothing is listening now

	c := NewClient(deadURL, "test@example.com", "api-token")
	_, err := c.GetSpace(context.Background(), "ENG")

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var rl *httpx.RateLimitError
	if errors.As(err, &rl) {
		t.Fatalf("network exhaustion must not be a *httpx.RateLimitError, got %v", err)
	}
	classified := httpx.ClassifyError(err)
	if classified.Code != output.ExitNetwork {
		t.Errorf("ClassifyError code = %d, want ExitNetwork (%d); err=%v", classified.Code, output.ExitNetwork, err)
	}
}

func TestClient_RetrySucceedsAfterTransientFailure(t *testing.T) {
	// 503 on the first attempt, 200 with a valid body on the second. The call
	// must recover and return the decoded result.
	old := baseRetryDelay
	baseRetryDelay = time.Millisecond
	defer func() { baseRetryDelay = old }()

	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"results": [{"id": "12345", "key": "ENG", "name": "Engineering"}]}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	space, err := c.GetSpace(context.Background(), "ENG")
	if err != nil {
		t.Fatalf("GetSpace() unexpected error: %v", err)
	}
	if calls != 2 {
		t.Errorf("server calls = %d, want 2 (one failure, one success)", calls)
	}
	if space == nil || space.ID != "12345" || space.Key != "ENG" || space.Name != "Engineering" {
		t.Errorf("space = %+v, want ID=12345 Key=ENG Name=Engineering", space)
	}
}

func TestClient_ListPages_LinkHeaderWinsOverBody(t *testing.T) {
	// When both a rel="next" Link header and a body _links.next are present with
	// different cursors, the header cursor wins.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", `<https://x/wiki/api/v2/spaces/S/pages?cursor=HDR>; rel="next"`)
		w.Write([]byte(`{
			"results": [{"id": "1", "title": "Page One"}],
			"_links": {"next": "/wiki/api/v2/spaces/12345/pages?cursor=BODY"}
		}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	_, nextCursor, err := c.ListPages(context.Background(), "12345", "", 25)
	if err != nil {
		t.Fatalf("ListPages() error: %v", err)
	}
	if nextCursor != "HDR" {
		t.Errorf("nextCursor = %q, want HDR (header precedence)", nextCursor)
	}
}

func TestClient_ListPages_BodyCursorWhenNoLinkHeader(t *testing.T) {
	// With no Link header, the body's _links.next cursor is used.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"results": [{"id": "1", "title": "Page One"}],
			"_links": {"next": "/wiki/api/v2/spaces/12345/pages?cursor=BODY"}
		}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	_, nextCursor, err := c.ListPages(context.Background(), "12345", "", 25)
	if err != nil {
		t.Fatalf("ListPages() error: %v", err)
	}
	if nextCursor != "BODY" {
		t.Errorf("nextCursor = %q, want BODY", nextCursor)
	}
}

func TestClient_TraceEmission(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results": [{"id": "1", "key": "ENG", "name": "Engineering"}]}`))
	}))
	defer server.Close()

	var buf bytes.Buffer
	ctx := httpx.WithTracer(context.Background(), httpx.NewJSONLinesTracer(&buf))

	c := NewClient(server.URL, "test@example.com", "api-token")
	if _, err := c.GetSpace(ctx, "ENG"); err != nil {
		t.Fatalf("GetSpace() error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, `"kind":"request"`) {
		t.Errorf("expected a request trace event, got: %s", out)
	}
	if !strings.Contains(out, `"endpoint":"/wiki/api/v2/spaces"`) {
		t.Errorf("expected endpoint in trace event, got: %s", out)
	}
}

func TestClient_Search(t *testing.T) {
	var gotCQL, gotLimit string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/rest/api/search" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		gotCQL = r.URL.Query().Get("cql")
		gotLimit = r.URL.Query().Get("limit")
		w.Write([]byte(`{
			"results": [
				{
					"content": {"id": "123", "title": "My Page", "type": "page", "space": {"key": "ENG"}},
					"excerpt": "some excerpt",
					"url": "/wiki/spaces/ENG/pages/123"
				}
			],
			"_links": {"next": "/wiki/rest/api/search?cursor=NEXT"}
		}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	results, nextCursor, err := c.Search(context.Background(), "type=page", "", 0)
	if err != nil {
		t.Fatalf("Search() error: %v", err)
	}
	if gotCQL != "type=page" {
		t.Errorf("cql = %q, want type=page", gotCQL)
	}
	if gotLimit != "25" {
		t.Errorf("limit = %q, want 25 (default)", gotLimit)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	r := results[0]
	if r.ID != "123" || r.Title != "My Page" || r.Type != "page" {
		t.Errorf("result = %+v, want ID=123 Title=My Page Type=page", r)
	}
	if r.SpaceKey != "ENG" {
		t.Errorf("SpaceKey = %q, want ENG", r.SpaceKey)
	}
	if r.Excerpt != "some excerpt" {
		t.Errorf("Excerpt = %q, want some excerpt", r.Excerpt)
	}
	if r.URL != "/wiki/spaces/ENG/pages/123" {
		t.Errorf("URL = %q, want /wiki/spaces/ENG/pages/123 (verbatim)", r.URL)
	}
	if nextCursor != "NEXT" {
		t.Errorf("nextCursor = %q, want NEXT", nextCursor)
	}
}

func TestClient_GetFooterComments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/api/v2/pages/123/footer-comments" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Write([]byte(`{
			"results": [
				{
					"id": "c1",
					"body": {"storage": {"value": "<p>Nice</p>"}},
					"version": {"authorId": "user1", "createdAt": "2024-01-15T10:30:00.000Z"},
					"_links": {"webui": "/spaces/ENG/pages/123?focusedCommentId=c1"}
				}
			],
			"_links": {"next": "/wiki/api/v2/pages/123/footer-comments?cursor=NEXT"}
		}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	comments, nextCursor, err := c.GetFooterComments(context.Background(), "123", "", 25)
	if err != nil {
		t.Fatalf("GetFooterComments() error: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("got %d comments, want 1", len(comments))
	}
	cm := comments[0]
	if cm.ID != "c1" {
		t.Errorf("ID = %q, want c1", cm.ID)
	}
	if cm.Kind != "footer" {
		t.Errorf("Kind = %q, want footer", cm.Kind)
	}
	if cm.Body != "<p>Nice</p>" {
		t.Errorf("Body = %q, want <p>Nice</p>", cm.Body)
	}
	if cm.AuthorID != "user1" {
		t.Errorf("AuthorID = %q, want user1", cm.AuthorID)
	}
	if cm.CreatedAt != "2024-01-15T10:30:00.000Z" {
		t.Errorf("CreatedAt = %q, want 2024-01-15T10:30:00.000Z", cm.CreatedAt)
	}
	wantURL := server.URL + "/wiki/spaces/ENG/pages/123?focusedCommentId=c1"
	if cm.WebURL != wantURL {
		t.Errorf("WebURL = %q, want %q", cm.WebURL, wantURL)
	}
	if nextCursor != "NEXT" {
		t.Errorf("nextCursor = %q, want NEXT", nextCursor)
	}
}

func TestClient_GetFooterComments_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	_, _, err := c.GetFooterComments(context.Background(), "123", "", 25)
	if !errors.Is(err, ErrPageNotFound) {
		t.Fatalf("expected ErrPageNotFound, got %v", err)
	}
}

func TestClient_GetInlineComments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/api/v2/pages/123/inline-comments" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Write([]byte(`{
			"results": [
				{"id": "c9", "body": {"storage": {"value": "<p>Hmm</p>"}}, "version": {"authorId": "u2"}}
			]
		}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	comments, _, err := c.GetInlineComments(context.Background(), "123", "", 25)
	if err != nil {
		t.Fatalf("GetInlineComments() error: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("got %d comments, want 1", len(comments))
	}
	if comments[0].Kind != "inline" {
		t.Errorf("Kind = %q, want inline", comments[0].Kind)
	}
	if comments[0].ID != "c9" {
		t.Errorf("ID = %q, want c9", comments[0].ID)
	}
}

func TestClient_GetInlineComments_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	_, _, err := c.GetInlineComments(context.Background(), "123", "", 25)
	if !errors.Is(err, ErrPageNotFound) {
		t.Fatalf("expected ErrPageNotFound, got %v", err)
	}
}

func TestClient_GetLabels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/api/v2/pages/123/labels" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Write([]byte(`{
			"results": [
				{"id": "l1", "name": "backend", "prefix": "global"},
				{"id": "l2", "name": "wip", "prefix": "my"}
			],
			"_links": {"next": "/wiki/api/v2/pages/123/labels?cursor=NEXT"}
		}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	labels, nextCursor, err := c.GetLabels(context.Background(), "123", "", 25)
	if err != nil {
		t.Fatalf("GetLabels() error: %v", err)
	}
	if len(labels) != 2 {
		t.Fatalf("got %d labels, want 2", len(labels))
	}
	if labels[0].ID != "l1" || labels[0].Name != "backend" || labels[0].Prefix != "global" {
		t.Errorf("labels[0] = %+v, want ID=l1 Name=backend Prefix=global", labels[0])
	}
	if labels[1].Name != "wip" {
		t.Errorf("labels[1].Name = %q, want wip", labels[1].Name)
	}
	if nextCursor != "NEXT" {
		t.Errorf("nextCursor = %q, want NEXT", nextCursor)
	}
}

func TestClient_GetLabels_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	_, _, err := c.GetLabels(context.Background(), "123", "", 25)
	if !errors.Is(err, ErrPageNotFound) {
		t.Fatalf("expected ErrPageNotFound, got %v", err)
	}
}

func TestClient_GetUser(t *testing.T) {
	tests := []struct {
		name            string
		response        string
		statusCode      int
		wantAccountID   string
		wantDisplayName string
		wantEmail       string
		wantErr         error
	}{
		{
			name:            "success",
			response:        `{"accountId":"5b10","displayName":"Ada","email":"ada@example.com"}`,
			statusCode:      http.StatusOK,
			wantAccountID:   "5b10",
			wantDisplayName: "Ada",
			wantEmail:       "ada@example.com",
		},
		{
			name:            "publicName fallback",
			response:        `{"accountId":"5b10","displayName":"","publicName":"Ada L"}`,
			statusCode:      http.StatusOK,
			wantAccountID:   "5b10",
			wantDisplayName: "Ada L",
		},
		{
			name:       "not found",
			response:   `{"message":"not found"}`,
			statusCode: http.StatusNotFound,
			wantErr:    ErrUserNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/wiki/rest/api/user" {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				if got := r.URL.Query().Get("accountId"); got != "5b10" {
					t.Errorf("accountId = %q, want 5b10", got)
				}
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.response))
			}))
			defer server.Close()

			c := NewClient(server.URL, "test@example.com", "api-token")
			user, err := c.GetUser(context.Background(), "5b10")

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("GetUser() error = %v, wantErr %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Errorf("GetUser() unexpected error: %v", err)
				return
			}
			if user.AccountID != tt.wantAccountID {
				t.Errorf("AccountID = %q, want %q", user.AccountID, tt.wantAccountID)
			}
			if user.DisplayName != tt.wantDisplayName {
				t.Errorf("DisplayName = %q, want %q", user.DisplayName, tt.wantDisplayName)
			}
			if user.Email != tt.wantEmail {
				t.Errorf("Email = %q, want %q", user.Email, tt.wantEmail)
			}
		})
	}
}

func TestClient_AuthHeader(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte(`{"results": [{"id": "1", "key": "ENG", "name": "Engineering"}]}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	_, _ = c.GetSpace(context.Background(), "ENG")

	if gotAuth == "" {
		t.Error("Authorization header not set")
	}
	// Should be Basic auth with email:token
	if gotAuth[:6] != "Basic " {
		t.Errorf("expected Basic auth, got: %s", gotAuth)
	}
}
