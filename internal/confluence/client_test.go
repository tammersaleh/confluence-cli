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
		name       string
		spaceID    string
		responses  []string
		statusCode int
		wantPages  []Page
		wantErr    bool
	}{
		{
			name:    "single page response",
			spaceID: "12345",
			responses: []string{`{
				"results": [
					{"id": "1", "title": "Page One", "parentId": ""},
					{"id": "2", "title": "Page Two", "parentId": "1"}
				]
			}`},
			statusCode: http.StatusOK,
			wantPages: []Page{
				{ID: "1", Title: "Page One", ParentID: ""},
				{ID: "2", Title: "Page Two", ParentID: "1"},
			},
		},
		{
			name:    "paginated response",
			spaceID: "12345",
			responses: []string{
				`{
					"results": [{"id": "1", "title": "Page One", "parentId": ""}],
					"_links": {"next": "/wiki/api/v2/spaces/12345/pages?cursor=abc"}
				}`,
				`{
					"results": [{"id": "2", "title": "Page Two", "parentId": "1"}]
				}`,
			},
			statusCode: http.StatusOK,
			wantPages: []Page{
				{ID: "1", Title: "Page One", ParentID: ""},
				{ID: "2", Title: "Page Two", ParentID: "1"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCount := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if callCount >= len(tt.responses) {
					t.Fatalf("unexpected call count: %d", callCount)
				}
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.responses[callCount]))
				callCount++
			}))
			defer server.Close()

			c := NewClient(server.URL, "test@example.com", "api-token")
			pages, err := c.GetPages(context.Background(), tt.spaceID)

			if tt.wantErr {
				if err == nil {
					t.Error("GetPages() expected error, got nil")
				}
				return
			}
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
