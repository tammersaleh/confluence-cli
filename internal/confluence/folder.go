package confluence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// folderResponse is the v2 folder shape (GET/POST /wiki/api/v2/folders).
// createdAt is a RawMessage because the live API returns it as epoch
// milliseconds (a JSON number) even though the docs describe an ISO string;
// normalizeTimestamp accepts both.
type folderResponse struct {
	ID         string          `json:"id"`
	Title      string          `json:"title"`
	SpaceID    string          `json:"spaceId"`
	Status     string          `json:"status"`
	ParentID   string          `json:"parentId"`
	ParentType string          `json:"parentType"`
	AuthorID   string          `json:"authorId"`
	CreatedAt  json.RawMessage `json:"createdAt"`
	Version    struct {
		Number    int    `json:"number"`
		CreatedAt string `json:"createdAt"`
	} `json:"version"`
	Links struct {
		WebUI string `json:"webui"`
	} `json:"_links"`
}

func (r folderResponse) toFolder(c *client) *Folder {
	return &Folder{
		ID:         r.ID,
		Title:      r.Title,
		SpaceID:    r.SpaceID,
		Status:     r.Status,
		ParentID:   r.ParentID,
		ParentType: r.ParentType,
		Version:    r.Version.Number,
		AuthorID:   r.AuthorID,
		CreatedAt:  normalizeTimestamp(r.CreatedAt),
		ModifiedAt: r.Version.CreatedAt,
		WebURL:     c.absWebURL(r.Links.WebUI),
	}
}

// normalizeTimestamp turns a raw JSON timestamp into an ISO 8601 string. A
// JSON string is returned verbatim; a JSON integer is treated as epoch
// milliseconds and formatted in UTC with millisecond precision. Anything else
// (null, absent, malformed) yields "".
func normalizeTimestamp(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	if strings.HasPrefix(s, `"`) {
		var str string
		if err := json.Unmarshal(raw, &str); err != nil {
			return ""
		}
		return str
	}
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

// folderNotFound retags a 404 StatusError as ErrFolderNotFound while keeping
// the endpoint and upstream message; any other error passes through.
func folderNotFound(err error) error {
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	var se *StatusError
	if errors.As(err, &se) {
		se.Err = ErrFolderNotFound
		return se
	}
	return ErrFolderNotFound
}

func (c *client) GetFolder(ctx context.Context, folderID string) (*Folder, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/wiki/api/v2/folders/%s", folderID), nil)
	if err != nil {
		return nil, folderNotFound(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result folderResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return result.toFolder(c), nil
}

// GetFolderAncestors returns the ancestor chain of folderID, root-most first.
// Like GetAncestors it drains pagination and fails closed on a malformed next
// link, because page-move cycle detection depends on a complete chain.
func (c *client) GetFolderAncestors(ctx context.Context, folderID string) ([]Page, error) {
	return c.getAncestors(ctx, fmt.Sprintf("/wiki/api/v2/folders/%s/ancestors", folderID), folderNotFound)
}

func (c *client) CreateFolder(ctx context.Context, p CreateFolderParams) (*Folder, error) {
	payload := map[string]any{
		"spaceId": p.SpaceID,
		"title":   p.Title,
	}
	if p.ParentID != "" {
		payload["parentId"] = p.ParentID
	}

	resp, err := c.doJSON(ctx, http.MethodPost, "/wiki/api/v2/folders", nil, payload)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var result folderResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return result.toFolder(c), nil
}

func (c *client) DeleteFolder(ctx context.Context, folderID string) error {
	resp, err := c.doRequest(ctx, http.MethodDelete, fmt.Sprintf("/wiki/api/v2/folders/%s", folderID), nil)
	if err != nil {
		return folderNotFound(err)
	}
	_ = resp.Body.Close()
	return nil
}
