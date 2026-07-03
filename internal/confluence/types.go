package confluence

import (
	"context"
	"errors"
	"fmt"
	"io"
)

var (
	ErrSpaceNotFound      = errors.New("space not found")
	ErrPageNotFound       = errors.New("page not found")       // used by GetPage in a later commit
	ErrAttachmentNotFound = errors.New("attachment not found") // 404 from attachment-by-id lookups
	ErrNotFound           = errors.New("not found")            // generic 404 from doRequest
	ErrUnauthorized       = errors.New("unauthorized")
	ErrForbidden          = errors.New("forbidden")
	ErrAPIError           = errors.New("API error")
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

type CurrentUser struct {
	AccountID   string
	DisplayName string
	Email       string // may be empty if the site hides email
}

type Space struct {
	ID         string
	Key        string
	Name       string
	Type       string // "global", "personal", etc.
	Status     string // "current", "archived"
	HomepageID string // ID of the space's homepage
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

type APIBodyFormat string

const (
	BodyFormatStorage  APIBodyFormat = "storage"
	BodyFormatAtlasDoc APIBodyFormat = "atlas_doc_format"
	BodyFormatView     APIBodyFormat = "view"
)

// PageDetail is a single page fetched for `page get`. Body is the raw value the
// API returned for the requested BodyFormat: XHTML for storage, HTML for view,
// and a JSON-encoded ADF string for atlas_doc_format (the CLI decides how to
// present it). Distinct from PageContent, which is storage-only and sync-shaped.
type PageDetail struct {
	ID         string
	Title      string
	SpaceID    string
	Version    int
	AuthorID   string
	CreatedAt  string // ISO 8601
	ModifiedAt string // ISO 8601 (version.createdAt)
	WebURL     string
	Body       string
	BodyFormat APIBodyFormat
}

type Client interface {
	GetCurrentUser(ctx context.Context) (*CurrentUser, error)
	GetSpace(ctx context.Context, spaceKey string) (*Space, error)
	GetSpaceByID(ctx context.Context, spaceID string) (*Space, error)
	ListSpaces(ctx context.Context, cursor string, limit int) (spaces []Space, nextCursor string, err error)
	GetPages(ctx context.Context, spaceID string) ([]Page, error)
	ListPages(ctx context.Context, spaceID, cursor string, limit int) (pages []Page, nextCursor string, err error)
	ListChildren(ctx context.Context, pageID, cursor string, limit int) (pages []Page, nextCursor string, err error)
	GetAncestors(ctx context.Context, pageID string) ([]Page, error)
	GetPageContent(ctx context.Context, pageID string) (*PageContent, error)
	GetPage(ctx context.Context, pageID string, format APIBodyFormat) (*PageDetail, error)
	GetAttachments(ctx context.Context, pageID string) ([]Attachment, error)
	GetAttachmentByID(ctx context.Context, attachmentID string) (*Attachment, error)
	DownloadAttachment(ctx context.Context, attachment Attachment) (io.ReadCloser, error)
	GetContentParent(ctx context.Context, id string, contentType string) (*Page, error)
}
