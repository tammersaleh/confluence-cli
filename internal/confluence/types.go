package confluence

import (
	"context"
	"errors"
	"fmt"
	"io"
)

var (
	ErrSpaceNotFound = errors.New("space not found")
	ErrPageNotFound  = errors.New("page not found") // used by GetPage in a later commit
	ErrNotFound      = errors.New("not found")      // generic 404 from doRequest
	ErrUnauthorized  = errors.New("unauthorized")
	ErrForbidden     = errors.New("forbidden")
	ErrAPIError      = errors.New("API error")
)

// StatusError is a typed transport error returned by doRequest for non-2xx
// responses. Err is one of ErrUnauthorized/ErrForbidden/ErrNotFound/ErrAPIError,
// so errors.Is against those sentinels works via Unwrap.
type StatusError struct {
	StatusCode int
	Endpoint   string // the API path, e.g. /wiki/api/v2/pages/123
	Err        error
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%v: status %d on %s", e.Err, e.StatusCode, e.Endpoint)
}

func (e *StatusError) Unwrap() error { return e.Err }

type Space struct {
	ID   string
	Key  string
	Name string
}

type Page struct {
	ID         string
	Title      string
	ParentID   string
	ParentType string // "page", "database", "folder", or "" (unknown/root)
	Type       string // "page", "database", "folder"
}

type PageContent struct {
	ID         string
	Title      string
	Body       string // Confluence storage format (XHTML)
	Version    int
	Author     string // Display name of creator
	AuthorID   string // Account ID of creator
	CreatedAt  string // ISO 8601 timestamp
	ModifiedAt string // ISO 8601 timestamp of last modification
	WebURL     string // Browser URL to view the page
}

type Attachment struct {
	ID          string
	Title       string
	MediaType   string
	DownloadURL string
}

type Client interface {
	GetSpace(ctx context.Context, spaceKey string) (*Space, error)
	GetPages(ctx context.Context, spaceID string) ([]Page, error)
	GetPageContent(ctx context.Context, pageID string) (*PageContent, error)
	GetAttachments(ctx context.Context, pageID string) ([]Attachment, error)
	DownloadAttachment(ctx context.Context, attachment Attachment) (io.ReadCloser, error)
	GetContentParent(ctx context.Context, id string, contentType string) (*Page, error)
}
