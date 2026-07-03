package confluence

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tammersaleh/confluence-sync/internal/httpx"
)

const (
	maxRetries    = 5
	maxRetryDelay = 30 * time.Second
)

// baseRetryDelay is a var (not const) so tests can shrink it to keep the retry
// path fast. Runtime value is always 500ms.
var baseRetryDelay = 500 * time.Millisecond

func isRetryable(statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests, // 429
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout:      // 504
		return true
	}
	return false
}

func retryDelay(attempt int) time.Duration {
	delay := baseRetryDelay * time.Duration(1<<uint(attempt)) // exponential: 500ms, 1s, 2s, 4s, 8s...
	if delay > maxRetryDelay {
		delay = maxRetryDelay
	}
	// Add jitter: +/- 25%
	jitter := time.Duration(rand.Int64N(int64(delay) / 2))
	delay = delay - delay/4 + jitter
	return delay
}

type client struct {
	baseURL    string
	httpClient *http.Client
	authHeader string
}

func NewClient(baseURL, email, apiToken string) Client {
	auth := base64.StdEncoding.EncodeToString([]byte(email + ":" + apiToken))
	return &client{
		baseURL:    baseURL,
		httpClient: http.DefaultClient,
		authHeader: "Basic " + auth,
	}
}

// statusToError maps a non-2xx status to a *StatusError, or nil for 2xx.
func statusToError(statusCode int, path string) *StatusError {
	var sentinel error
	switch {
	case statusCode == http.StatusUnauthorized:
		sentinel = ErrUnauthorized
	case statusCode == http.StatusForbidden:
		sentinel = ErrForbidden
	case statusCode == http.StatusNotFound:
		sentinel = ErrNotFound
	case statusCode >= 400:
		sentinel = ErrAPIError
	default:
		return nil
	}
	return &StatusError{StatusCode: statusCode, Endpoint: path, Err: sentinel}
}

func (c *client) doRequest(ctx context.Context, method, path string, query url.Values) (*http.Response, error) {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	tracer := httpx.TracerFrom(ctx)

	var lastErr error
	lastStatus := 0
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := retryDelay(attempt - 1)
			tracer.Event("retry_wait", map[string]any{
				"endpoint": path,
				"attempt":  attempt + 1,
				"delay_ms": delay.Milliseconds(),
			})
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		req, err := http.NewRequestWithContext(ctx, method, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", c.authHeader)
		req.Header.Set("Accept", "application/json")

		start := time.Now()
		resp, err := c.httpClient.Do(req)
		latency := time.Since(start)
		if err != nil {
			lastErr = err
			lastStatus = 0
			tracer.Event("request", map[string]any{
				"endpoint":   path,
				"method":     method,
				"attempt":    attempt + 1,
				"status":     0,
				"latency_ms": latency.Milliseconds(),
				"error":      err.Error(),
			})
			continue
		}

		statusErr := statusToError(resp.StatusCode, path)
		errStr := ""
		if statusErr != nil {
			errStr = statusErr.Error()
		}
		tracer.Event("request", map[string]any{
			"endpoint":   path,
			"method":     method,
			"attempt":    attempt + 1,
			"status":     resp.StatusCode,
			"latency_ms": latency.Milliseconds(),
			"error":      errStr,
		})

		if isRetryable(resp.StatusCode) {
			_ = resp.Body.Close()
			lastErr = statusErr
			lastStatus = resp.StatusCode
			continue
		}

		if statusErr != nil {
			_ = resp.Body.Close()
			return nil, statusErr
		}

		return resp, nil
	}

	// Retry exhaustion. If the last retryable response was a 429, surface a
	// typed rate-limit error; otherwise wrap the last 5xx/transport error.
	if lastStatus == http.StatusTooManyRequests {
		wrapped := lastErr
		if wrapped == nil {
			wrapped = fmt.Errorf("%w: status %d", ErrAPIError, http.StatusTooManyRequests)
		}
		return nil, &httpx.RateLimitError{Endpoint: path, Retries: maxRetries, Err: wrapped}
	}
	return nil, fmt.Errorf("after %d retries: %w", maxRetries, lastErr)
}

type currentUserResponse struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
	PublicName  string `json:"publicName"`
	Email       string `json:"email"`
}

func (c *client) GetCurrentUser(ctx context.Context) (*CurrentUser, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/wiki/rest/api/user/current", nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var result currentUserResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	displayName := result.DisplayName
	if displayName == "" {
		displayName = result.PublicName
	}

	return &CurrentUser{
		AccountID:   result.AccountID,
		DisplayName: displayName,
		Email:       result.Email,
	}, nil
}

type spaceResponse struct {
	ID         string `json:"id"`
	Key        string `json:"key"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Status     string `json:"status"`
	HomepageID string `json:"homepageId"`
}

type spacesResponse struct {
	Results []spaceResponse `json:"results"`
}

func (c *client) GetSpace(ctx context.Context, spaceKey string) (*Space, error) {
	query := url.Values{}
	query.Set("keys", spaceKey)

	resp, err := c.doRequest(ctx, http.MethodGet, "/wiki/api/v2/spaces", query)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrSpaceNotFound
		}
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var result spacesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	if len(result.Results) == 0 {
		return nil, ErrSpaceNotFound
	}

	s := result.Results[0]
	return &Space{
		ID:         s.ID,
		Key:        s.Key,
		Name:       s.Name,
		Type:       s.Type,
		Status:     s.Status,
		HomepageID: s.HomepageID,
	}, nil
}

// GetSpaceByID fetches a single space by its numeric ID via the
// /wiki/api/v2/spaces/{id} endpoint. A 404 maps to ErrSpaceNotFound.
func (c *client) GetSpaceByID(ctx context.Context, spaceID string) (*Space, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/wiki/api/v2/spaces/%s", spaceID), nil)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrSpaceNotFound
		}
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var result spaceResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return &Space{
		ID:         result.ID,
		Key:        result.Key,
		Name:       result.Name,
		Type:       result.Type,
		Status:     result.Status,
		HomepageID: result.HomepageID,
	}, nil
}

type pagesResponse struct {
	Results []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	} `json:"results"`
	Links struct {
		Next string `json:"next"`
	} `json:"_links"`
}

type pageResponse struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	ParentID   string `json:"parentId"`
	ParentType string `json:"parentType"`
}

func (c *client) GetPages(ctx context.Context, spaceID string) ([]Page, error) {
	var allPages []Page
	path := fmt.Sprintf("/wiki/api/v2/spaces/%s/pages", spaceID)
	query := url.Values{}
	query.Set("limit", "250")

	for {
		resp, err := c.doRequest(ctx, http.MethodGet, path, query)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, ErrSpaceNotFound
			}
			return nil, err
		}

		var result pagesResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("decoding response: %w", err)
		}
		_ = resp.Body.Close()

		for _, p := range result.Results {
			allPages = append(allPages, Page{
				ID:    p.ID,
				Title: p.Title,
			})
		}

		if result.Links.Next == "" {
			break
		}

		// Parse next URL to get cursor
		nextURL, err := url.Parse(result.Links.Next)
		if err != nil {
			break
		}
		query = nextURL.Query()
		path = nextURL.Path
	}

	// The /spaces/{id}/pages endpoint doesn't reliably return parentId,
	// so we fetch each page individually to get parent info.
	for i := range allPages {
		parentID, parentType, err := c.getPageParent(ctx, allPages[i].ID)
		if err != nil {
			return nil, fmt.Errorf("getting parent for page %s: %w", allPages[i].ID, err)
		}
		allPages[i].ParentID = parentID
		allPages[i].ParentType = parentType
		allPages[i].Type = "page"
	}

	// Resolve non-page parents (databases, folders) by fetching them and
	// adding synthetic nodes to the page list. Repeat until all parents
	// resolve to known nodes or root.
	known := make(map[string]bool, len(allPages))
	for _, p := range allPages {
		known[p.ID] = true
	}

	for {
		var toResolve []Page
		for _, p := range allPages {
			if p.ParentID != "" && !known[p.ParentID] && (p.ParentType == "database" || p.ParentType == "folder") {
				toResolve = append(toResolve, p)
			}
		}
		if len(toResolve) == 0 {
			break
		}

		// Deduplicate by parent ID
		seen := make(map[string]bool)
		for _, p := range toResolve {
			if seen[p.ParentID] {
				continue
			}
			seen[p.ParentID] = true

			parent, err := c.GetContentParent(ctx, p.ParentID, p.ParentType)
			if err != nil {
				return nil, fmt.Errorf("resolving %s parent %s: %w", p.ParentType, p.ParentID, err)
			}
			allPages = append(allPages, *parent)
			known[parent.ID] = true
		}
	}

	return allPages, nil
}

type listPagesResponse struct {
	Results []struct {
		ID         string `json:"id"`
		Title      string `json:"title"`
		ParentID   string `json:"parentId"`
		ParentType string `json:"parentType"`
	} `json:"results"`
	Links struct {
		Next string `json:"next"`
	} `json:"_links"`
}

// cursorFromNext extracts the "cursor" query-param value from a next-page URL
// (either a full URL or a path with query). Returns "" if absent or unparsable.
func cursorFromNext(next string) string {
	if next == "" {
		return ""
	}
	u, err := url.Parse(next)
	if err != nil {
		return ""
	}
	return u.Query().Get("cursor")
}

// ListPages returns exactly one API page of results for the space. Unlike
// GetPages (the sync crawl), it does NOT resolve parents via N+1 lookups or add
// synthetic parent nodes: parentId/parentType are decoded best-effort from the
// list response when present.
func (c *client) ListPages(ctx context.Context, spaceID, cursor string, limit int) (pages []Page, nextCursor string, err error) {
	if limit <= 0 {
		limit = 25
	}
	query := url.Values{}
	query.Set("limit", fmt.Sprintf("%d", limit))
	if cursor != "" {
		query.Set("cursor", cursor)
	}

	path := fmt.Sprintf("/wiki/api/v2/spaces/%s/pages", spaceID)
	resp, err := c.doRequest(ctx, http.MethodGet, path, query)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, "", ErrSpaceNotFound
		}
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var result listPagesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, "", fmt.Errorf("decoding response: %w", err)
	}

	pages = make([]Page, 0, len(result.Results))
	for _, p := range result.Results {
		pages = append(pages, Page{
			ID:         p.ID,
			Title:      p.Title,
			ParentID:   p.ParentID,
			ParentType: p.ParentType,
			Type:       "page",
		})
	}

	// Prefer the Link response header if present, else the body's _links.next.
	next := result.Links.Next
	if hdr := resp.Header.Get("Link"); hdr != "" {
		if c := cursorFromNext(parseLinkHeaderNext(hdr)); c != "" {
			return pages, c, nil
		}
	}
	return pages, cursorFromNext(next), nil
}

// parseLinkHeaderNext returns the URL of the rel="next" entry in an RFC 5988
// Link header, or "" if none.
func parseLinkHeaderNext(header string) string {
	for _, part := range strings.Split(header, ",") {
		segs := strings.Split(part, ";")
		if len(segs) < 2 {
			continue
		}
		isNext := false
		for _, s := range segs[1:] {
			if strings.Contains(s, `rel="next"`) || strings.Contains(s, "rel=next") {
				isNext = true
				break
			}
		}
		if !isNext {
			continue
		}
		u := strings.TrimSpace(segs[0])
		u = strings.TrimPrefix(u, "<")
		u = strings.TrimSuffix(u, ">")
		return u
	}
	return ""
}

func (c *client) getPageParent(ctx context.Context, pageID string) (parentID, parentType string, err error) {
	resp, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/wiki/api/v2/pages/%s", pageID), nil)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", "", ErrPageNotFound
		}
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var result pageResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", fmt.Errorf("decoding response: %w", err)
	}

	return result.ParentID, result.ParentType, nil
}

type pageContentResponse struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CreatedAt string `json:"createdAt"`
	AuthorID  string `json:"authorId"`
	Body      struct {
		Storage struct {
			Value string `json:"value"`
		} `json:"storage"`
	} `json:"body"`
	Version struct {
		Number    int    `json:"number"`
		CreatedAt string `json:"createdAt"`
		AuthorID  string `json:"authorId"`
	} `json:"version"`
	Links struct {
		WebUI string `json:"webui"`
	} `json:"_links"`
}

func (c *client) GetPageContent(ctx context.Context, pageID string) (*PageContent, error) {
	query := url.Values{}
	query.Set("body-format", "storage")

	resp, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/wiki/api/v2/pages/%s", pageID), query)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrPageNotFound
		}
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var result pageContentResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	webURL := result.Links.WebUI
	if webURL != "" && !strings.HasPrefix(webURL, "http") {
		if !strings.HasPrefix(webURL, "/wiki") {
			webURL = "/wiki" + webURL
		}
		webURL = c.baseURL + webURL
	}

	return &PageContent{
		ID:         result.ID,
		Title:      result.Title,
		Body:       result.Body.Storage.Value,
		Version:    result.Version.Number,
		AuthorID:   result.AuthorID,
		CreatedAt:  result.CreatedAt,
		ModifiedAt: result.Version.CreatedAt,
		WebURL:     webURL,
	}, nil
}

type pageDetailResponse struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	SpaceID   string `json:"spaceId"`
	AuthorID  string `json:"authorId"`
	CreatedAt string `json:"createdAt"`
	Body      struct {
		Storage struct {
			Value string `json:"value"`
		} `json:"storage"`
		AtlasDocFormat struct {
			Value string `json:"value"`
		} `json:"atlas_doc_format"`
		View struct {
			Value string `json:"value"`
		} `json:"view"`
	} `json:"body"`
	Version struct {
		Number    int    `json:"number"`
		CreatedAt string `json:"createdAt"`
	} `json:"version"`
	Links struct {
		WebUI string `json:"webui"`
	} `json:"_links"`
}

func (c *client) GetPage(ctx context.Context, pageID string, format APIBodyFormat) (*PageDetail, error) {
	switch format {
	case BodyFormatStorage, BodyFormatAtlasDoc, BodyFormatView:
	default:
		return nil, fmt.Errorf("unsupported body format %q", format)
	}

	query := url.Values{}
	query.Set("body-format", string(format))

	resp, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/wiki/api/v2/pages/%s", pageID), query)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrPageNotFound
		}
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var result pageDetailResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	var body string
	switch format {
	case BodyFormatStorage:
		body = result.Body.Storage.Value
	case BodyFormatAtlasDoc:
		body = result.Body.AtlasDocFormat.Value
	case BodyFormatView:
		body = result.Body.View.Value
	}

	webURL := result.Links.WebUI
	if webURL != "" && !strings.HasPrefix(webURL, "http") {
		if !strings.HasPrefix(webURL, "/wiki") {
			webURL = "/wiki" + webURL
		}
		webURL = c.baseURL + webURL
	}

	return &PageDetail{
		ID:         result.ID,
		Title:      result.Title,
		SpaceID:    result.SpaceID,
		Version:    result.Version.Number,
		AuthorID:   result.AuthorID,
		CreatedAt:  result.CreatedAt,
		ModifiedAt: result.Version.CreatedAt,
		WebURL:     webURL,
		Body:       body,
		BodyFormat: format,
	}, nil
}

type attachmentsResponse struct {
	Results []struct {
		ID           string `json:"id"`
		Title        string `json:"title"`
		MediaType    string `json:"mediaType"`
		DownloadLink string `json:"downloadLink"`
	} `json:"results"`
	Links struct {
		Next string `json:"next"`
	} `json:"_links"`
}

func (c *client) GetAttachments(ctx context.Context, pageID string) ([]Attachment, error) {
	var allAttachments []Attachment
	path := fmt.Sprintf("/wiki/api/v2/pages/%s/attachments", pageID)
	query := url.Values{}
	query.Set("limit", "250")

	for {
		resp, err := c.doRequest(ctx, http.MethodGet, path, query)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, ErrPageNotFound
			}
			return nil, err
		}

		var result attachmentsResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("decoding response: %w", err)
		}
		_ = resp.Body.Close()

		for _, a := range result.Results {
			allAttachments = append(allAttachments, Attachment{
				ID:          a.ID,
				Title:       a.Title,
				MediaType:   a.MediaType,
				DownloadURL: a.DownloadLink,
			})
		}

		if result.Links.Next == "" {
			break
		}

		nextURL, err := url.Parse(result.Links.Next)
		if err != nil {
			break
		}
		query = nextURL.Query()
		path = nextURL.Path
	}

	return allAttachments, nil
}

type attachmentResponse struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	MediaType    string `json:"mediaType"`
	DownloadLink string `json:"downloadLink"`
}

// GetAttachmentByID fetches a single attachment by its ID via the
// /wiki/api/v2/attachments/{id} endpoint. A 404 maps to ErrAttachmentNotFound.
func (c *client) GetAttachmentByID(ctx context.Context, attachmentID string) (*Attachment, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/wiki/api/v2/attachments/%s", attachmentID), nil)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrAttachmentNotFound
		}
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var result attachmentResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return &Attachment{
		ID:          result.ID,
		Title:       result.Title,
		MediaType:   result.MediaType,
		DownloadURL: result.DownloadLink,
	}, nil
}

func (c *client) GetContentParent(ctx context.Context, id string, contentType string) (*Page, error) {
	var apiPath string
	switch contentType {
	case "database":
		apiPath = fmt.Sprintf("/wiki/api/v2/databases/%s", id)
	case "folder":
		apiPath = fmt.Sprintf("/wiki/api/v2/folders/%s", id)
	default:
		return nil, fmt.Errorf("unsupported content type: %s", contentType)
	}

	resp, err := c.doRequest(ctx, http.MethodGet, apiPath, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var result pageResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return &Page{
		ID:         result.ID,
		Title:      result.Title,
		ParentID:   result.ParentID,
		ParentType: result.ParentType,
		Type:       contentType,
	}, nil
}

func (c *client) DownloadAttachment(ctx context.Context, attachment Attachment) (io.ReadCloser, error) {
	// Download URLs from the API are relative to /wiki, not the base URL
	downloadPath := attachment.DownloadURL
	if !strings.HasPrefix(downloadPath, "/wiki") {
		downloadPath = "/wiki" + downloadPath
	}
	u := c.baseURL + downloadPath

	tracer := httpx.TracerFrom(ctx)

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := retryDelay(attempt - 1)
			tracer.Event("retry_wait", map[string]any{
				"endpoint": downloadPath,
				"attempt":  attempt + 1,
				"delay_ms": delay.Milliseconds(),
			})
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", c.authHeader)

		start := time.Now()
		resp, err := c.httpClient.Do(req)
		latency := time.Since(start)
		if err != nil {
			lastErr = err
			tracer.Event("request", map[string]any{
				"endpoint":   downloadPath,
				"method":     http.MethodGet,
				"attempt":    attempt + 1,
				"status":     0,
				"latency_ms": latency.Milliseconds(),
				"error":      err.Error(),
			})
			continue
		}

		tracer.Event("request", map[string]any{
			"endpoint":   downloadPath,
			"method":     http.MethodGet,
			"attempt":    attempt + 1,
			"status":     resp.StatusCode,
			"latency_ms": latency.Milliseconds(),
		})

		if isRetryable(resp.StatusCode) {
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("%w: status %d downloading %s", ErrAPIError, resp.StatusCode, u)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("%w: status %d downloading %s", ErrAPIError, resp.StatusCode, u)
		}

		return resp.Body, nil
	}

	return nil, fmt.Errorf("after %d retries: %w", maxRetries, lastErr)
}
