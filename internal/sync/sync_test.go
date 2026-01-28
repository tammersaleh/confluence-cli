package sync

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"testing"

	"github.com/tammersaleh/confluence-sync/internal/confluence"
	"github.com/tammersaleh/confluence-sync/internal/filesystem"
)

type mockClient struct {
	space       *confluence.Space
	pages       []confluence.Page
	contents    map[string]*confluence.PageContent
	attachments map[string][]confluence.Attachment
	downloads   map[string][]byte
}

func (m *mockClient) GetSpace(ctx context.Context, spaceKey string) (*confluence.Space, error) {
	if m.space == nil {
		return nil, confluence.ErrSpaceNotFound
	}
	return m.space, nil
}

func (m *mockClient) GetPages(ctx context.Context, spaceID string) ([]confluence.Page, error) {
	return m.pages, nil
}

func (m *mockClient) GetPageContent(ctx context.Context, pageID string) (*confluence.PageContent, error) {
	if c, ok := m.contents[pageID]; ok {
		return c, nil
	}
	return &confluence.PageContent{ID: pageID, Title: "Page", Body: "<p>Content</p>"}, nil
}

func (m *mockClient) GetAttachments(ctx context.Context, pageID string) ([]confluence.Attachment, error) {
	return m.attachments[pageID], nil
}

func (m *mockClient) DownloadAttachment(ctx context.Context, att confluence.Attachment) (io.ReadCloser, error) {
	data := m.downloads[att.Title]
	if data == nil {
		data = []byte("file content")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func TestSync_SingleLeafPage(t *testing.T) {
	client := &mockClient{
		space: &confluence.Space{ID: "123", Key: "TEST", Name: "Test Space"},
		pages: []confluence.Page{
			{ID: "1", Title: "My Page", ParentID: ""},
		},
		contents: map[string]*confluence.PageContent{
			"1": {ID: "1", Title: "My Page", Body: "<p>Hello world</p>"},
		},
	}

	fs := filesystem.NewMemory()
	syncer := New(client, fs)

	err := syncer.Sync(context.Background(), "TEST", "/output", Options{})
	if err != nil {
		t.Fatalf("Sync error: %v", err)
	}

	files := fs.Files()
	indexPath := filepath.Join("/output", "index.md")
	content, ok := files[indexPath]
	if !ok {
		t.Fatalf("expected %s to exist, got files: %v", indexPath, files)
	}
	if !bytes.Contains(content, []byte("Hello world")) {
		t.Errorf("content = %q, want to contain 'Hello world'", string(content))
	}
}

func TestSync_PageWithChildren(t *testing.T) {
	client := &mockClient{
		space: &confluence.Space{ID: "123", Key: "TEST", Name: "Test Space"},
		pages: []confluence.Page{
			{ID: "1", Title: "Parent", ParentID: ""},
			{ID: "2", Title: "Child One", ParentID: "1"},
			{ID: "3", Title: "Child Two", ParentID: "1"},
		},
		contents: map[string]*confluence.PageContent{
			"1": {ID: "1", Title: "Parent", Body: "<p>Parent content</p>"},
			"2": {ID: "2", Title: "Child One", Body: "<p>Child one content</p>"},
			"3": {ID: "3", Title: "Child Two", Body: "<p>Child two content</p>"},
		},
	}

	fs := filesystem.NewMemory()
	syncer := New(client, fs)

	err := syncer.Sync(context.Background(), "TEST", "/output", Options{})
	if err != nil {
		t.Fatalf("Sync error: %v", err)
	}

	files := fs.Files()

	// Parent becomes index.md in root
	if _, ok := files["/output/index.md"]; !ok {
		t.Error("expected /output/index.md to exist")
	}

	// Children become separate .md files
	if _, ok := files["/output/child-one.md"]; !ok {
		t.Error("expected /output/child-one.md to exist")
	}
	if _, ok := files["/output/child-two.md"]; !ok {
		t.Error("expected /output/child-two.md to exist")
	}
}

func TestSync_NestedHierarchy(t *testing.T) {
	client := &mockClient{
		space: &confluence.Space{ID: "123", Key: "TEST", Name: "Test Space"},
		pages: []confluence.Page{
			{ID: "1", Title: "Root", ParentID: ""},
			{ID: "2", Title: "Section", ParentID: "1"},
			{ID: "3", Title: "Article", ParentID: "2"},
		},
		contents: map[string]*confluence.PageContent{
			"1": {ID: "1", Title: "Root", Body: "<p>Root</p>"},
			"2": {ID: "2", Title: "Section", Body: "<p>Section</p>"},
			"3": {ID: "3", Title: "Article", Body: "<p>Article</p>"},
		},
	}

	fs := filesystem.NewMemory()
	syncer := New(client, fs)

	err := syncer.Sync(context.Background(), "TEST", "/output", Options{})
	if err != nil {
		t.Fatalf("Sync error: %v", err)
	}

	files := fs.Files()

	// Root becomes /output/index.md
	if _, ok := files["/output/index.md"]; !ok {
		t.Error("expected /output/index.md")
	}

	// Section has children, so becomes /output/section/index.md
	if _, ok := files["/output/section/index.md"]; !ok {
		t.Error("expected /output/section/index.md")
	}

	// Article is a leaf under section
	if _, ok := files["/output/section/article.md"]; !ok {
		t.Error("expected /output/section/article.md")
	}
}

func TestSync_PageWithAttachments(t *testing.T) {
	client := &mockClient{
		space: &confluence.Space{ID: "123", Key: "TEST", Name: "Test Space"},
		pages: []confluence.Page{
			{ID: "1", Title: "Page With Files", ParentID: ""},
		},
		contents: map[string]*confluence.PageContent{
			"1": {ID: "1", Title: "Page With Files", Body: `<ac:image><ri:attachment ri:filename="diagram.png"/></ac:image>`},
		},
		attachments: map[string][]confluence.Attachment{
			"1": {
				{ID: "att1", Title: "diagram.png", DownloadURL: "/download/diagram.png"},
			},
		},
		downloads: map[string][]byte{
			"diagram.png": []byte("PNG DATA"),
		},
	}

	fs := filesystem.NewMemory()
	syncer := New(client, fs)

	err := syncer.Sync(context.Background(), "TEST", "/output", Options{})
	if err != nil {
		t.Fatalf("Sync error: %v", err)
	}

	files := fs.Files()

	// Page with attachments should be promoted to directory
	if _, ok := files["/output/index.md"]; !ok {
		t.Error("expected /output/index.md")
	}

	// Attachment should be in _attachments
	attPath := "/output/_attachments/diagram.png"
	if content, ok := files[attPath]; !ok {
		t.Errorf("expected %s to exist", attPath)
	} else if string(content) != "PNG DATA" {
		t.Errorf("attachment content = %q, want 'PNG DATA'", string(content))
	}

	// Markdown should reference attachment with correct path
	mdContent := string(files["/output/index.md"])
	if !bytes.Contains([]byte(mdContent), []byte("_attachments/diagram.png")) {
		t.Errorf("markdown should reference _attachments/diagram.png, got: %s", mdContent)
	}
}

func TestSync_CleanOption(t *testing.T) {
	client := &mockClient{
		space: &confluence.Space{ID: "123", Key: "TEST", Name: "Test Space"},
		pages: []confluence.Page{
			{ID: "1", Title: "New Page", ParentID: ""},
		},
	}

	fs := filesystem.NewMemory()
	// Pre-existing file
	fs.WriteFile("/output/old-file.md", []byte("old content"), 0644)

	syncer := New(client, fs)

	err := syncer.Sync(context.Background(), "TEST", "/output", Options{Clean: true})
	if err != nil {
		t.Fatalf("Sync error: %v", err)
	}

	files := fs.Files()

	// Old file should be gone
	if _, ok := files["/output/old-file.md"]; ok {
		t.Error("expected old-file.md to be removed with --clean")
	}

	// New file should exist
	if _, ok := files["/output/index.md"]; !ok {
		t.Error("expected index.md to exist")
	}
}

func TestSync_DryRunOption(t *testing.T) {
	client := &mockClient{
		space: &confluence.Space{ID: "123", Key: "TEST", Name: "Test Space"},
		pages: []confluence.Page{
			{ID: "1", Title: "Test Page", ParentID: ""},
		},
	}

	fs := filesystem.NewMemory()
	syncer := New(client, fs)

	err := syncer.Sync(context.Background(), "TEST", "/output", Options{DryRun: true})
	if err != nil {
		t.Fatalf("Sync error: %v", err)
	}

	files := fs.Files()

	// No files should be written in dry-run mode
	if len(files) != 0 {
		t.Errorf("expected no files in dry-run, got %v", files)
	}
}

func TestSync_FilenameCollisions(t *testing.T) {
	client := &mockClient{
		space: &confluence.Space{ID: "123", Key: "TEST", Name: "Test Space"},
		pages: []confluence.Page{
			{ID: "1", Title: "Root", ParentID: ""},
			{ID: "2", Title: "Test!", ParentID: "1"},
			{ID: "3", Title: "Test?", ParentID: "1"},
			{ID: "4", Title: "Test.", ParentID: "1"},
		},
	}

	fs := filesystem.NewMemory()
	syncer := New(client, fs)

	err := syncer.Sync(context.Background(), "TEST", "/output", Options{})
	if err != nil {
		t.Fatalf("Sync error: %v", err)
	}

	files := fs.Files()

	// All three pages should have unique filenames
	found := map[string]bool{}
	for path := range files {
		found[path] = true
	}

	// Should have index.md and three test-*.md files
	expectedCount := 4 // index.md + test.md + test-2.md + test-3.md
	if len(files) != expectedCount {
		t.Errorf("expected %d files, got %d: %v", expectedCount, len(files), files)
	}
}

func TestSync_Frontmatter(t *testing.T) {
	client := &mockClient{
		space: &confluence.Space{ID: "123", Key: "TEST", Name: "Test Space"},
		pages: []confluence.Page{
			{ID: "456", Title: "My Page", ParentID: ""},
		},
		contents: map[string]*confluence.PageContent{
			"456": {
				ID:         "456",
				Title:      "My Page",
				Body:       "<p>Hello world</p>",
				Version:    3,
				Author:     "John Doe",
				AuthorID:   "user123",
				CreatedAt:  "2024-01-15T10:30:00Z",
				ModifiedAt: "2024-06-20T14:45:00Z",
				WebURL:     "https://acme.atlassian.net/wiki/spaces/TEST/pages/456/My+Page",
			},
		},
	}

	fs := filesystem.NewMemory()
	syncer := New(client, fs)

	err := syncer.Sync(context.Background(), "TEST", "/output", Options{})
	if err != nil {
		t.Fatalf("Sync error: %v", err)
	}

	content := string(fs.Files()["/output/index.md"])

	// Should start with YAML frontmatter
	if !bytes.HasPrefix([]byte(content), []byte("---\n")) {
		t.Errorf("expected file to start with YAML frontmatter, got:\n%s", content)
	}

	// Check for required metadata fields
	checks := []string{
		"confluence_page_id: \"456\"",
		"title: \"My Page\"",
		"author: \"John Doe\"",
		"confluence_url:",
		"version: 3",
	}
	for _, check := range checks {
		if !bytes.Contains([]byte(content), []byte(check)) {
			t.Errorf("expected frontmatter to contain %q, got:\n%s", check, content)
		}
	}

	// Should have closing frontmatter delimiter
	if !bytes.Contains([]byte(content), []byte("\n---\n")) {
		t.Errorf("expected closing frontmatter delimiter, got:\n%s", content)
	}

	// Content should follow frontmatter
	if !bytes.Contains([]byte(content), []byte("Hello world")) {
		t.Errorf("expected body content after frontmatter, got:\n%s", content)
	}
}
