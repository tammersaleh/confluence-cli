package cli

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tammersaleh/confluence-sync/internal/output"
)

// attachmentListServer serves /wiki/api/v2/pages/{id}/attachments. Any id in
// notFound returns 404 (-> ErrPageNotFound); otherwise a single attachment
// whose downloadLink lacks the /wiki prefix, exercising the absolutizer.
func attachmentListServer(t *testing.T, notFound map[string]bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/wiki/api/v2/pages/"
		if !strings.HasPrefix(r.URL.Path, prefix) || !strings.HasSuffix(r.URL.Path, "/attachments") {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), "/attachments")
		if notFound[id] {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"results":[{"id":"att1","title":"diagram.png","mediaType":"image/png","downloadLink":"/download/attachments/` + id + `/diagram.png"}],"_links":{"next":""}}`))
	}))
}

func TestAttachmentList_BareID(t *testing.T) {
	clearCredEnv(t)
	server := attachmentListServer(t, nil)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &AttachmentListCmd{Page: "123"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 2 {
		t.Fatalf("expected 1 row + 1 meta, got %d: %q", len(lines), out.String())
	}
	row := lines[0]
	if row["id"] != "att1" || row["title"] != "diagram.png" || row["media_type"] != "image/png" {
		t.Errorf("row shape unexpected: %v", row)
	}
	if row["page_id"] != "123" {
		t.Errorf("page_id = %v, want 123", row["page_id"])
	}
	wantURL := server.URL + "/wiki/download/attachments/123/diagram.png"
	if row["download_url"] != wantURL {
		t.Errorf("download_url = %v, want %v", row["download_url"], wantURL)
	}
}

func TestAttachmentList_URLDerivesSite(t *testing.T) {
	clearCredEnv(t)
	server := attachmentListServer(t, nil)
	defer server.Close()

	// No CONFLUENCE_SITE: the site is derived from the URL arg.
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &AttachmentListCmd{Page: server.URL + "/wiki/spaces/ENG/pages/777/Title"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 2 {
		t.Fatalf("expected 1 row + 1 meta, got %d: %q", len(lines), out.String())
	}
	if lines[0]["page_id"] != "777" {
		t.Errorf("page_id = %v, want 777", lines[0]["page_id"])
	}
}

func TestAttachmentList_PageNotFound(t *testing.T) {
	clearCredEnv(t)
	server := attachmentListServer(t, map[string]bool{"404": true})
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &AttachmentListCmd{Page: "404"}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "page_not_found" {
		t.Errorf("Err = %q, want page_not_found", oErr.Err)
	}
}

// attachmentDownloadServer serves /wiki/api/v2/attachments/{id} (metadata) and
// the download bytes at the attachment's downloadLink path. Any id in notFound
// returns 404 for the metadata lookup.
func attachmentDownloadServer(t *testing.T, notFound map[string]bool, payload string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const metaPrefix = "/wiki/api/v2/attachments/"
		switch {
		case strings.HasPrefix(r.URL.Path, metaPrefix):
			id := strings.TrimPrefix(r.URL.Path, metaPrefix)
			if notFound[id] {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(`{"id":"` + id + `","title":"report.txt","mediaType":"text/plain","downloadLink":"/download/attachments/1/report.txt"}`))
		case r.URL.Path == "/wiki/download/attachments/1/report.txt":
			_, _ = w.Write([]byte(payload))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestAttachmentDownload_WritesFile(t *testing.T) {
	clearCredEnv(t)
	const payload = "hello attachment body"
	server := attachmentDownloadServer(t, nil, payload)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	dest := filepath.Join(t.TempDir(), "out.txt")
	cmd := &AttachmentDownloadCmd{ID: "att1", Out: dest}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading downloaded file: %v", err)
	}
	if string(got) != payload {
		t.Errorf("file content = %q, want %q", got, payload)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 2 {
		t.Fatalf("expected 1 row + 1 meta, got %d: %q", len(lines), out.String())
	}
	row := lines[0]
	if row["id"] != "att1" || row["title"] != "report.txt" || row["media_type"] != "text/plain" {
		t.Errorf("row shape unexpected: %v", row)
	}
	if row["path"] != dest {
		t.Errorf("path = %v, want %v", row["path"], dest)
	}
	if row["bytes"] != float64(len(payload)) {
		t.Errorf("bytes = %v, want %d", row["bytes"], len(payload))
	}
}

func TestAttachmentDownload_NotFound(t *testing.T) {
	clearCredEnv(t)
	server := attachmentDownloadServer(t, map[string]bool{"404": true}, "")
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &AttachmentDownloadCmd{ID: "404"}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "attachment_not_found" {
		t.Errorf("Err = %q, want attachment_not_found", oErr.Err)
	}
}

func TestAttachmentDownload_URLRejected(t *testing.T) {
	clearCredEnv(t)
	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &AttachmentDownloadCmd{ID: "https://acme.atlassian.net/wiki/spaces/ENG/pages/1/A"}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "invalid_input" || oErr.Code != output.ExitGeneral {
		t.Errorf("got Err=%q Code=%d, want invalid_input/%d", oErr.Err, oErr.Code, output.ExitGeneral)
	}
}
