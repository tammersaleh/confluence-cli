package confluence

import (
	"context"
	"errors"
	"io"
)

var (
	ErrSpaceNotFound = errors.New("space not found")
	ErrUnauthorized  = errors.New("unauthorized")
	ErrForbidden     = errors.New("forbidden")
	ErrAPIError      = errors.New("API error")
)

type Space struct {
	ID   string
	Key  string
	Name string
}

type Page struct {
	ID       string
	Title    string
	ParentID string
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
}
