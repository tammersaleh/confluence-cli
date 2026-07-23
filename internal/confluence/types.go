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
	ErrUserNotFound       = errors.New("user not found")       // 404 from user-by-id lookups
	ErrCommentNotFound    = errors.New("comment not found")    // 404 from comment-children lookups
	ErrUnauthorized       = errors.New("unauthorized")
	ErrForbidden          = errors.New("forbidden")
	ErrAPIError           = errors.New("API error")
	// ErrVersionConflict is returned only by UpdatePage when the server rejects
	// the write with HTTP 409 (a concurrent edit landed between the caller's
	// read and this write). It is not applied to 409s from other endpoints.
	ErrVersionConflict = errors.New("version conflict")
	// ErrLiveConvertFailed is returned only for HTTP-200 logical or protocol
	// failures from the undocumented live-conversion endpoint (GraphQL errors,
	// success=false, or an unrecognized response shape). Transport failures and
	// non-2xx statuses keep their StatusError/sentinel classification.
	ErrLiveConvertFailed = errors.New("live conversion failed")
)

// StatusError is a typed transport error returned by doRequest for non-2xx
// responses. Err is one of ErrUnauthorized/ErrForbidden/ErrNotFound/ErrAPIError,
// so errors.Is against those sentinels works via Unwrap.
type StatusError struct {
	StatusCode int
	Endpoint   string // the API path, e.g. /wiki/api/v2/pages/123
	Err        error
	Message    string // best-effort human message extracted from the response body
}

func (e *StatusError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%v: status %d on %s: %s", e.Err, e.StatusCode, e.Endpoint, e.Message)
	}
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

// Descendant is one node under a page in the descendants crawl. Unlike
// ListChildren (direct children only), the descendants endpoint returns every
// level below the page and carries depth (1 for direct children) and parentId.
type Descendant struct {
	ID       string
	Title    string
	Type     string // "page", "database", "folder"
	ParentID string
	Depth    int
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

// SearchResult is one hit from a CQL search. URL is returned verbatim from the
// API (it may be relative to the site root); the caller presents it as-is.
type SearchResult struct {
	ID       string
	Title    string
	Type     string // "page", "blogpost", "comment", etc.
	SpaceKey string
	Excerpt  string
	URL      string
}

// Comment is a footer or inline comment on a page. Kind is "footer" or "inline".
// Body is the storage-format (XHTML) value. The three inline-only fields are
// populated verbatim from the API for inline comments and left empty for footer
// comments; ResolutionStatus is NOT normalized (only exact "resolved" is a
// resolved anchor - see the page update guard).
type Comment struct {
	ID                string
	Kind              string // "footer" or "inline"
	Body              string
	AuthorID          string
	CreatedAt         string // ISO 8601
	WebURL            string // Browser URL to view the comment
	ResolutionStatus  string // inline only: "open"|"reopened"|"resolved"|"dangling" (raw)
	OriginalSelection string // inline only: the highlighted anchor text
	InlineMarkerRef   string // inline only: the body marker id for the anchor
}

// Label is a label attached to a page.
type Label struct {
	ID     string
	Name   string
	Prefix string
}

// InlineCommentSelection anchors an inline comment to on-page text. Text is the
// literal string to attach to; MatchCount is the total number of occurrences of
// Text in the page body (>=1) and MatchIndex is which occurrence to anchor to
// (0-based, < MatchCount).
type InlineCommentSelection struct {
	Text       string // the on-page text to anchor to
	MatchCount int    // total occurrences of Text in the page body (>=1)
	MatchIndex int    // which occurrence, 0-based
}

type APIBodyFormat string

const (
	BodyFormatStorage  APIBodyFormat = "storage"
	BodyFormatAtlasDoc APIBodyFormat = "atlas_doc_format"
	BodyFormatView     APIBodyFormat = "view"
)

// PageSubtypeLive is the page subtype for a Confluence "live doc". A regular
// page has no subtype (the API returns it null/absent).
const PageSubtypeLive = "live"

// PageDetail is a single page fetched for `page get`. Body is the raw value the
// API returned for the requested BodyFormat: XHTML for storage, HTML for view,
// and a JSON-encoded ADF string for atlas_doc_format (the CLI decides how to
// present it). Distinct from PageContent, which is storage-only and sync-shaped.
type PageDetail struct {
	ID         string
	Title      string
	SpaceID    string
	Status     string // "current", "draft", etc. - preserved verbatim on update
	ParentID   string // parent page id, "" for a space root
	Version    int
	AuthorID   string
	CreatedAt  string // ISO 8601
	ModifiedAt string // ISO 8601 (version.createdAt)
	WebURL     string
	Body       string
	BodyFormat APIBodyFormat
	Subtype    string // "live" for a live doc; empty for a regular page
}

// WriteBody is the body payload for a create/update. Representation is one of
// BodyFormatStorage or BodyFormatAtlasDoc; Value is the raw content in that
// representation.
type WriteBody struct {
	Representation APIBodyFormat
	Value          string
}

// PageRecord is the server's response to a create/update page call. It carries
// the identifying and version fields returned, not the full body.
type PageRecord struct {
	ID        string
	Title     string
	SpaceID   string
	ParentID  string
	Version   int
	AuthorID  string
	CreatedAt string
	WebURL    string
	Subtype   string // "live" for a live doc; empty for a regular page
}

// CreatePageParams are the inputs to CreatePage. Status defaults to "current"
// when empty. Body is optional (nil creates an empty page).
type CreatePageParams struct {
	SpaceID  string
	Title    string
	ParentID string
	Status   string
	Body     *WriteBody
	Subtype  string // "live" creates a live doc; empty creates a regular page
}

// UpdatePageParams are the inputs to UpdatePage. Version is the NEW version
// number to send (the caller computes it, typically current+1). Status is
// preserved verbatim from the current page and must be supplied (UpdatePage no
// longer defaults it to "current", which would silently change a draft).
// ParentID reparents the page when non-nil; nil omits parentId entirely so an
// ordinary update never moves the page.
type UpdatePageParams struct {
	ID       string
	Title    string
	Status   string
	Version  int
	Body     WriteBody
	ParentID *string
}

type Client interface {
	GetCurrentUser(ctx context.Context) (*CurrentUser, error)
	GetUser(ctx context.Context, accountID string) (*CurrentUser, error)
	Search(ctx context.Context, cql, cursor string, limit int) (results []SearchResult, nextCursor string, err error)
	GetFooterComments(ctx context.Context, pageID, cursor string, limit int) (comments []Comment, nextCursor string, err error)
	// GetInlineComments lists inline comments. resolutionStatus, when non-empty,
	// server-side filters by "open"|"reopened"|"resolved"|"dangling"; empty
	// returns every inline comment.
	GetInlineComments(ctx context.Context, pageID, cursor string, limit int, resolutionStatus string) (comments []Comment, nextCursor string, err error)
	GetCommentChildren(ctx context.Context, commentID, kind, cursor string, limit int) (comments []Comment, nextCursor string, err error)
	GetLabels(ctx context.Context, pageID, cursor string, limit int) (labels []Label, nextCursor string, err error)
	GetSpace(ctx context.Context, spaceKey string) (*Space, error)
	GetSpaceByID(ctx context.Context, spaceID string) (*Space, error)
	ListSpaces(ctx context.Context, cursor string, limit int) (spaces []Space, nextCursor string, err error)
	GetPages(ctx context.Context, spaceID string) ([]Page, error)
	ListPages(ctx context.Context, spaceID, cursor string, limit int) (pages []Page, nextCursor string, err error)
	ListChildren(ctx context.Context, pageID, cursor string, limit int) (pages []Page, nextCursor string, err error)
	GetDescendants(ctx context.Context, pageID, cursor string, limit int) (descendants []Descendant, nextCursor string, err error)
	GetAncestors(ctx context.Context, pageID string) ([]Page, error)
	GetPageContent(ctx context.Context, pageID string) (*PageContent, error)
	GetPage(ctx context.Context, pageID string, format APIBodyFormat) (*PageDetail, error)
	GetAttachments(ctx context.Context, pageID string) ([]Attachment, error)
	GetAttachmentByID(ctx context.Context, attachmentID string) (*Attachment, error)
	DownloadAttachment(ctx context.Context, attachment Attachment) (io.ReadCloser, error)
	GetContentParent(ctx context.Context, id string, contentType string) (*Page, error)
	CreatePage(ctx context.Context, p CreatePageParams) (*PageRecord, error)
	UpdatePage(ctx context.Context, p UpdatePageParams) (*PageRecord, error)
	DeletePage(ctx context.Context, pageID string) error
	// ConvertPageToLive converts an existing page to a live doc. It uses an
	// undocumented internal Confluence endpoint (no public API exists); see
	// the implementation for caveats.
	ConvertPageToLive(ctx context.Context, pageID string) error
	AddFooterComment(ctx context.Context, pageID string, body WriteBody) (*Comment, error)
	AddInlineComment(ctx context.Context, pageID string, body WriteBody, sel InlineCommentSelection) (*Comment, error)
	AddLabel(ctx context.Context, pageID, label string) (*Label, error)
	RemoveLabel(ctx context.Context, pageID, label string) error
	UploadAttachment(ctx context.Context, pageID, filePath string) (*Attachment, error)
}
