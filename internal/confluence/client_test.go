package confluence

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
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
					"name": "Engineering"
				}]
			}`,
			statusCode: http.StatusOK,
			wantSpace: &Space{
				ID:   "12345",
				Key:  "ENG",
				Name: "Engineering",
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
				if err != tt.wantErr {
					t.Errorf("GetSpace() error = %v, wantErr %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Errorf("GetSpace() unexpected error: %v", err)
				return
			}
			if space.ID != tt.wantSpace.ID || space.Key != tt.wantSpace.Key || space.Name != tt.wantSpace.Name {
				t.Errorf("GetSpace() = %+v, want %+v", space, tt.wantSpace)
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
