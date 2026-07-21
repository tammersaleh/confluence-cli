package confluence

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tammersaleh/confluence-cli/internal/httpx"
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

// errBodyLimit bounds how much of an error response body we read when
// extracting a human-readable message. 8KB is plenty for Confluence errors.
const errBodyLimit = 8 << 10

// reqOptions carries per-request configuration for doCore. bodyFactory is nil
// for GET-style requests; when set it is called fresh on EACH attempt so retries
// resend the body.
type reqOptions struct {
	query       url.Values
	headers     http.Header               // extra headers (Content-Type, X-Atlassian-Token, etc.)
	bodyFactory func() (io.Reader, error) // nil for bodyless requests; called per attempt
}

// doRequest performs a bodyless (typically GET) request. It delegates to doCore
// with a nil body, preserving the original signature and behavior.
func (c *client) doRequest(ctx context.Context, method, path string, query url.Values) (*http.Response, error) {
	return c.doCore(ctx, method, path, reqOptions{query: query})
}

// doCore is the shared request/retry/trace/status engine. It builds the URL from
// path + query, retries retryable statuses with backoff, emits trace events, and
// maps non-2xx responses to *StatusError (or *httpx.RateLimitError on 429
// exhaustion). When opts.bodyFactory is set it is invoked fresh on each attempt.
func (c *client) doCore(ctx context.Context, method, path string, opts reqOptions) (*http.Response, error) {
	u := c.baseURL + path
	if len(opts.query) > 0 {
		u += "?" + opts.query.Encode()
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

		var body io.Reader
		if opts.bodyFactory != nil {
			b, err := opts.bodyFactory()
			if err != nil {
				return nil, err
			}
			body = b
		}

		req, err := http.NewRequestWithContext(ctx, method, u, body)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", c.authHeader)
		req.Header.Set("Accept", "application/json")
		for k, vs := range opts.headers {
			for _, v := range vs {
				req.Header.Set(k, v)
			}
		}

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
			statusErr.Message = extractAPIMessage(resp.Body)
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

// extractAPIMessage reads a bounded prefix of an error response body and
// best-effort extracts a human message from Confluence's error JSON shapes:
// {"message":"..."} or {"errors":[{"title":"..."}]} or
// {"errors":[{"detail":"..."}]}. Returns "" on any parse failure.
func extractAPIMessage(body io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(body, errBodyLimit))
	if err != nil || len(data) == 0 {
		return ""
	}
	var parsed struct {
		Message string `json:"message"`
		Errors  []struct {
			Title  string `json:"title"`
			Detail string `json:"detail"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return ""
	}
	if parsed.Message != "" {
		return parsed.Message
	}
	for _, e := range parsed.Errors {
		if e.Title != "" {
			return e.Title
		}
		if e.Detail != "" {
			return e.Detail
		}
	}
	return ""
}

// doJSON sends payload as a JSON request body. The payload is marshaled once;
// bodyFactory returns a fresh bytes.Reader over those bytes on each attempt, so
// retries resend the identical body.
func (c *client) doJSON(ctx context.Context, method, path string, query url.Values, payload any) (*http.Response, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling request body: %w", err)
	}
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	headers.Set("Accept", "application/json")
	return c.doCore(ctx, method, path, reqOptions{
		query:   query,
		headers: headers,
		bodyFactory: func() (io.Reader, error) {
			return bytes.NewReader(b), nil
		},
	})
}

// doMultipart sends a multipart/form-data body built by build. The form is
// rendered once to fixed bytes (capturing the writer's boundary for the
// Content-Type header); bodyFactory returns a fresh reader over those bytes on
// each attempt so retries resend an identical body.
func (c *client) doMultipart(ctx context.Context, method, path string, query url.Values, build func(*multipart.Writer) error) (*http.Response, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := build(w); err != nil {
		return nil, fmt.Errorf("building multipart body: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("closing multipart body: %w", err)
	}
	b := buf.Bytes()

	headers := http.Header{}
	headers.Set("Content-Type", w.FormDataContentType())
	headers.Set("X-Atlassian-Token", "nocheck")
	headers.Set("Accept", "application/json")
	return c.doCore(ctx, method, path, reqOptions{
		query:   query,
		headers: headers,
		bodyFactory: func() (io.Reader, error) {
			return bytes.NewReader(b), nil
		},
	})
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

func (c *client) GetUser(ctx context.Context, accountID string) (*CurrentUser, error) {
	query := url.Values{}
	query.Set("accountId", accountID)

	resp, err := c.doRequest(ctx, http.MethodGet, "/wiki/rest/api/user", query)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrUserNotFound
		}
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

type searchResponse struct {
	Results []struct {
		Content struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Type  string `json:"type"`
			Space struct {
				Key string `json:"key"`
			} `json:"space"`
		} `json:"content"`
		Excerpt string `json:"excerpt"`
		URL     string `json:"url"`
	} `json:"results"`
	Links struct {
		Next string `json:"next"`
	} `json:"_links"`
}

// Search runs a CQL query via the v1 search endpoint and returns one API page of
// results. URLs are returned verbatim (they may be relative). Invalid CQL
// typically returns a 400, surfaced as a *StatusError wrapping ErrAPIError.
func (c *client) Search(ctx context.Context, cql, cursor string, limit int) (results []SearchResult, nextCursor string, err error) {
	if limit <= 0 {
		limit = 25
	}
	query := url.Values{}
	query.Set("cql", cql)
	query.Set("limit", fmt.Sprintf("%d", limit))
	if cursor != "" {
		query.Set("cursor", cursor)
	}

	resp, err := c.doRequest(ctx, http.MethodGet, "/wiki/rest/api/search", query)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var result searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, "", fmt.Errorf("decoding response: %w", err)
	}

	results = make([]SearchResult, 0, len(result.Results))
	for _, r := range result.Results {
		results = append(results, SearchResult{
			ID:       r.Content.ID,
			Title:    r.Content.Title,
			Type:     r.Content.Type,
			SpaceKey: r.Content.Space.Key,
			Excerpt:  r.Excerpt,
			URL:      r.URL,
		})
	}

	if hdr := resp.Header.Get("Link"); hdr != "" {
		if cur := cursorFromNext(parseLinkHeaderNext(hdr)); cur != "" {
			return results, cur, nil
		}
	}
	return results, cursorFromNext(result.Links.Next), nil
}

type commentsResponse struct {
	Results []struct {
		ID   string `json:"id"`
		Body struct {
			Storage struct {
				Value string `json:"value"`
			} `json:"storage"`
		} `json:"body"`
		Version struct {
			AuthorID  string `json:"authorId"`
			CreatedAt string `json:"createdAt"`
		} `json:"version"`
		Links struct {
			WebUI string `json:"webui"`
		} `json:"_links"`
	} `json:"results"`
	Links struct {
		Next string `json:"next"`
	} `json:"_links"`
}

// getComments fetches one API page of comments from the given v2 sub-resource
// ("footer-comments" or "inline-comments") and tags them with kind.
func (c *client) getComments(ctx context.Context, pageID, resource, kind, cursor string, limit int) (comments []Comment, nextCursor string, err error) {
	if limit <= 0 {
		limit = 25
	}
	query := url.Values{}
	query.Set("body-format", "storage")
	query.Set("limit", fmt.Sprintf("%d", limit))
	if cursor != "" {
		query.Set("cursor", cursor)
	}

	path := fmt.Sprintf("/wiki/api/v2/pages/%s/%s", pageID, resource)
	resp, err := c.doRequest(ctx, http.MethodGet, path, query)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, "", ErrPageNotFound
		}
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var result commentsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, "", fmt.Errorf("decoding response: %w", err)
	}

	comments = make([]Comment, 0, len(result.Results))
	for _, r := range result.Results {
		webURL := r.Links.WebUI
		if webURL != "" && !strings.HasPrefix(webURL, "http") {
			if !strings.HasPrefix(webURL, "/wiki") {
				webURL = "/wiki" + webURL
			}
			webURL = c.baseURL + webURL
		}
		comments = append(comments, Comment{
			ID:        r.ID,
			Kind:      kind,
			Body:      r.Body.Storage.Value,
			AuthorID:  r.Version.AuthorID,
			CreatedAt: r.Version.CreatedAt,
			WebURL:    webURL,
		})
	}

	if hdr := resp.Header.Get("Link"); hdr != "" {
		if cur := cursorFromNext(parseLinkHeaderNext(hdr)); cur != "" {
			return comments, cur, nil
		}
	}
	return comments, cursorFromNext(result.Links.Next), nil
}

func (c *client) GetFooterComments(ctx context.Context, pageID, cursor string, limit int) ([]Comment, string, error) {
	return c.getComments(ctx, pageID, "footer-comments", "footer", cursor, limit)
}

func (c *client) GetInlineComments(ctx context.Context, pageID, cursor string, limit int) ([]Comment, string, error) {
	return c.getComments(ctx, pageID, "inline-comments", "inline", cursor, limit)
}

// GetCommentChildren fetches one API page of direct replies to a comment. kind
// selects the resource ("footer" -> footer-comments, otherwise inline-comments)
// since replies live under the same resource as their parent; returned comments
// carry that same kind. A 404 maps to ErrCommentNotFound.
func (c *client) GetCommentChildren(ctx context.Context, commentID, kind, cursor string, limit int) (comments []Comment, nextCursor string, err error) {
	resource := "inline-comments"
	if kind == "footer" {
		resource = "footer-comments"
	}
	if limit <= 0 {
		limit = 25
	}
	query := url.Values{}
	query.Set("body-format", "storage")
	query.Set("limit", fmt.Sprintf("%d", limit))
	if cursor != "" {
		query.Set("cursor", cursor)
	}

	path := fmt.Sprintf("/wiki/api/v2/%s/%s/children", resource, commentID)
	resp, err := c.doRequest(ctx, http.MethodGet, path, query)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, "", ErrCommentNotFound
		}
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var result commentsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, "", fmt.Errorf("decoding response: %w", err)
	}

	comments = make([]Comment, 0, len(result.Results))
	for _, r := range result.Results {
		webURL := r.Links.WebUI
		if webURL != "" && !strings.HasPrefix(webURL, "http") {
			if !strings.HasPrefix(webURL, "/wiki") {
				webURL = "/wiki" + webURL
			}
			webURL = c.baseURL + webURL
		}
		comments = append(comments, Comment{
			ID:        r.ID,
			Kind:      kind,
			Body:      r.Body.Storage.Value,
			AuthorID:  r.Version.AuthorID,
			CreatedAt: r.Version.CreatedAt,
			WebURL:    webURL,
		})
	}

	if hdr := resp.Header.Get("Link"); hdr != "" {
		if cur := cursorFromNext(parseLinkHeaderNext(hdr)); cur != "" {
			return comments, cur, nil
		}
	}
	return comments, cursorFromNext(result.Links.Next), nil
}

type labelsResponse struct {
	Results []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Prefix string `json:"prefix"`
	} `json:"results"`
	Links struct {
		Next string `json:"next"`
	} `json:"_links"`
}

func (c *client) GetLabels(ctx context.Context, pageID, cursor string, limit int) (labels []Label, nextCursor string, err error) {
	if limit <= 0 {
		limit = 25
	}
	query := url.Values{}
	query.Set("limit", fmt.Sprintf("%d", limit))
	if cursor != "" {
		query.Set("cursor", cursor)
	}

	path := fmt.Sprintf("/wiki/api/v2/pages/%s/labels", pageID)
	resp, err := c.doRequest(ctx, http.MethodGet, path, query)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, "", ErrPageNotFound
		}
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var result labelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, "", fmt.Errorf("decoding response: %w", err)
	}

	labels = make([]Label, 0, len(result.Results))
	for _, l := range result.Results {
		labels = append(labels, Label{
			ID:     l.ID,
			Name:   l.Name,
			Prefix: l.Prefix,
		})
	}

	if hdr := resp.Header.Get("Link"); hdr != "" {
		if cur := cursorFromNext(parseLinkHeaderNext(hdr)); cur != "" {
			return labels, cur, nil
		}
	}
	return labels, cursorFromNext(result.Links.Next), nil
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

type childrenResponse struct {
	Results []struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Status string `json:"status"`
	} `json:"results"`
	Links struct {
		Next string `json:"next"`
	} `json:"_links"`
}

// ListChildren returns exactly one API page of direct children of pageID. The
// v2 children endpoint returns id/title/status/spaceId/childPosition but no
// body and no reliable parentId, so ParentID is left empty (all results are
// children of pageID anyway) and Type is always "page".
func (c *client) ListChildren(ctx context.Context, pageID, cursor string, limit int) (pages []Page, nextCursor string, err error) {
	if limit <= 0 {
		limit = 25
	}
	query := url.Values{}
	query.Set("limit", fmt.Sprintf("%d", limit))
	if cursor != "" {
		query.Set("cursor", cursor)
	}

	path := fmt.Sprintf("/wiki/api/v2/pages/%s/children", pageID)
	resp, err := c.doRequest(ctx, http.MethodGet, path, query)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, "", ErrPageNotFound
		}
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var result childrenResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, "", fmt.Errorf("decoding response: %w", err)
	}

	pages = make([]Page, 0, len(result.Results))
	for _, p := range result.Results {
		pages = append(pages, Page{
			ID:    p.ID,
			Title: p.Title,
			Type:  "page",
		})
	}

	// Prefer the Link response header if present, else the body's _links.next.
	if hdr := resp.Header.Get("Link"); hdr != "" {
		if cur := cursorFromNext(parseLinkHeaderNext(hdr)); cur != "" {
			return pages, cur, nil
		}
	}
	return pages, cursorFromNext(result.Links.Next), nil
}

type descendantsResponse struct {
	Results []struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Type     string `json:"type"`
		ParentID string `json:"parentId"`
		Depth    int    `json:"depth"`
	} `json:"results"`
	Links struct {
		Next string `json:"next"`
	} `json:"_links"`
}

// GetDescendants returns exactly one API page of descendants of pageID (every
// level below it, not just direct children). Each item carries depth (1 for a
// direct child) and parentId. Type is passed through from the API ("page",
// "database", "folder").
func (c *client) GetDescendants(ctx context.Context, pageID, cursor string, limit int) (descendants []Descendant, nextCursor string, err error) {
	if limit <= 0 {
		limit = 25
	}
	query := url.Values{}
	query.Set("limit", fmt.Sprintf("%d", limit))
	if cursor != "" {
		query.Set("cursor", cursor)
	}

	path := fmt.Sprintf("/wiki/api/v2/pages/%s/descendants", pageID)
	resp, err := c.doRequest(ctx, http.MethodGet, path, query)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, "", ErrPageNotFound
		}
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var result descendantsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, "", fmt.Errorf("decoding response: %w", err)
	}

	descendants = make([]Descendant, 0, len(result.Results))
	for _, d := range result.Results {
		descendants = append(descendants, Descendant{
			ID:       d.ID,
			Title:    d.Title,
			Type:     d.Type,
			ParentID: d.ParentID,
			Depth:    d.Depth,
		})
	}

	// Prefer the Link response header if present, else the body's _links.next.
	if hdr := resp.Header.Get("Link"); hdr != "" {
		if cur := cursorFromNext(parseLinkHeaderNext(hdr)); cur != "" {
			return descendants, cur, nil
		}
	}
	return descendants, cursorFromNext(result.Links.Next), nil
}

type ancestorsResponse struct {
	Results []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	} `json:"results"`
	Links struct {
		Next string `json:"next"`
	} `json:"_links"`
}

// GetAncestors returns the ancestor chain of pageID, root-most first (per
// Atlassian). The v2 ancestors endpoint returns limited fields (id, type;
// often no title), so only ID and Type are populated. Ancestor chains are
// short but pagination is followed if present.
func (c *client) GetAncestors(ctx context.Context, pageID string) ([]Page, error) {
	var ancestors []Page
	path := fmt.Sprintf("/wiki/api/v2/pages/%s/ancestors", pageID)
	query := url.Values{}

	for {
		resp, err := c.doRequest(ctx, http.MethodGet, path, query)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, ErrPageNotFound
			}
			return nil, err
		}

		var result ancestorsResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("decoding response: %w", err)
		}

		// Prefer the Link header, else the body's _links.next.
		next := result.Links.Next
		if hdr := resp.Header.Get("Link"); hdr != "" {
			if h := parseLinkHeaderNext(hdr); h != "" {
				next = h
			}
		}
		_ = resp.Body.Close()

		for _, a := range result.Results {
			ancestors = append(ancestors, Page{
				ID:   a.ID,
				Type: a.Type,
			})
		}

		if next == "" {
			break
		}
		nextURL, err := url.Parse(next)
		if err != nil {
			break
		}
		query = nextURL.Query()
		path = nextURL.Path
	}

	return ancestors, nil
}

type listSpacesResponse struct {
	Results []spaceResponse `json:"results"`
	Links   struct {
		Next string `json:"next"`
	} `json:"_links"`
}

// ListSpaces returns exactly one API page of spaces (no key filter). nextCursor
// comes from the Link header or the body's _links.next.
func (c *client) ListSpaces(ctx context.Context, cursor string, limit int) (spaces []Space, nextCursor string, err error) {
	if limit <= 0 {
		limit = 25
	}
	query := url.Values{}
	query.Set("limit", fmt.Sprintf("%d", limit))
	if cursor != "" {
		query.Set("cursor", cursor)
	}

	resp, err := c.doRequest(ctx, http.MethodGet, "/wiki/api/v2/spaces", query)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var result listSpacesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, "", fmt.Errorf("decoding response: %w", err)
	}

	spaces = make([]Space, 0, len(result.Results))
	for _, s := range result.Results {
		spaces = append(spaces, Space(s))
	}

	// Prefer the Link response header if present, else the body's _links.next.
	if hdr := resp.Header.Get("Link"); hdr != "" {
		if cur := cursorFromNext(parseLinkHeaderNext(hdr)); cur != "" {
			return spaces, cur, nil
		}
	}
	return spaces, cursorFromNext(result.Links.Next), nil
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

// absWebURL absolutizes a Confluence _links.webui value against the client's
// baseURL, prepending /wiki when missing. Empty and already-absolute values are
// returned unchanged.
func (c *client) absWebURL(webURL string) string {
	if webURL == "" || strings.HasPrefix(webURL, "http") {
		return webURL
	}
	if !strings.HasPrefix(webURL, "/wiki") {
		webURL = "/wiki" + webURL
	}
	return c.baseURL + webURL
}

// pageRecordResponse is the create/update page response shape.
type pageRecordResponse struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	SpaceID   string `json:"spaceId"`
	ParentID  string `json:"parentId"`
	AuthorID  string `json:"authorId"`
	CreatedAt string `json:"createdAt"`
	Subtype   string `json:"subtype"`
	Version   struct {
		Number int `json:"number"`
	} `json:"version"`
	Links struct {
		WebUI string `json:"webui"`
	} `json:"_links"`
}

func (r pageRecordResponse) toRecord(c *client) *PageRecord {
	return &PageRecord{
		ID:        r.ID,
		Title:     r.Title,
		SpaceID:   r.SpaceID,
		ParentID:  r.ParentID,
		Version:   r.Version.Number,
		AuthorID:  r.AuthorID,
		CreatedAt: r.CreatedAt,
		WebURL:    c.absWebURL(r.Links.WebUI),
		Subtype:   r.Subtype,
	}
}

// bodyPayload is the {"representation","value"} JSON object for a write body.
type bodyPayload struct {
	Representation string `json:"representation"`
	Value          string `json:"value"`
}

func (c *client) CreatePage(ctx context.Context, p CreatePageParams) (*PageRecord, error) {
	status := p.Status
	if status == "" {
		status = "current"
	}

	payload := map[string]any{
		"spaceId": p.SpaceID,
		"status":  status,
		"title":   p.Title,
	}
	if p.ParentID != "" {
		payload["parentId"] = p.ParentID
	}
	if p.Subtype != "" {
		payload["subtype"] = p.Subtype
	}
	if p.Body != nil {
		payload["body"] = bodyPayload{
			Representation: string(p.Body.Representation),
			Value:          p.Body.Value,
		}
	}

	resp, err := c.doJSON(ctx, http.MethodPost, "/wiki/api/v2/pages", nil, payload)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var result pageRecordResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return result.toRecord(c), nil
}

func (c *client) UpdatePage(ctx context.Context, p UpdatePageParams) (*PageRecord, error) {
	status := p.Status
	if status == "" {
		status = "current"
	}

	payload := map[string]any{
		"id":     p.ID,
		"status": status,
		"title":  p.Title,
		"version": map[string]any{
			"number": p.Version,
		},
		"body": bodyPayload{
			Representation: string(p.Body.Representation),
			Value:          p.Body.Value,
		},
	}

	resp, err := c.doJSON(ctx, http.MethodPut, fmt.Sprintf("/wiki/api/v2/pages/%s", p.ID), nil, payload)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrPageNotFound
		}
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var result pageRecordResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return result.toRecord(c), nil
}

func (c *client) DeletePage(ctx context.Context, pageID string) error {
	resp, err := c.doRequest(ctx, http.MethodDelete, fmt.Sprintf("/wiki/api/v2/pages/%s", pageID), nil)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrPageNotFound
		}
		return err
	}
	_ = resp.Body.Close()
	return nil
}

func (c *client) AddFooterComment(ctx context.Context, pageID string, body WriteBody) (*Comment, error) {
	payload := map[string]any{
		"pageId": pageID,
		"body": bodyPayload{
			Representation: string(body.Representation),
			Value:          body.Value,
		},
	}

	resp, err := c.doJSON(ctx, http.MethodPost, "/wiki/api/v2/footer-comments", nil, payload)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrPageNotFound
		}
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		ID   string `json:"id"`
		Body struct {
			Storage struct {
				Value string `json:"value"`
			} `json:"storage"`
		} `json:"body"`
		Links struct {
			WebUI string `json:"webui"`
		} `json:"_links"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return &Comment{
		ID:     result.ID,
		Kind:   "footer",
		Body:   result.Body.Storage.Value,
		WebURL: c.absWebURL(result.Links.WebUI),
	}, nil
}

// AddInlineComment creates an inline comment anchored to on-page text via the v2
// /wiki/api/v2/inline-comments endpoint. The inlineCommentProperties shape
// (textSelection/textSelectionMatchCount/textSelectionMatchIndex) follows the
// Atlassian docs. NOTE: this create shape has not been verified against a live
// site; it is exercised via httptest like the rest of the write surface. Live
// verification is pending.
func (c *client) AddInlineComment(ctx context.Context, pageID string, body WriteBody, sel InlineCommentSelection) (*Comment, error) {
	payload := map[string]any{
		"pageId": pageID,
		"body": bodyPayload{
			Representation: string(body.Representation),
			Value:          body.Value,
		},
		"inlineCommentProperties": map[string]any{
			"textSelection":           sel.Text,
			"textSelectionMatchCount": sel.MatchCount,
			"textSelectionMatchIndex": sel.MatchIndex,
		},
	}

	resp, err := c.doJSON(ctx, http.MethodPost, "/wiki/api/v2/inline-comments", nil, payload)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrPageNotFound
		}
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		ID   string `json:"id"`
		Body struct {
			Storage struct {
				Value string `json:"value"`
			} `json:"storage"`
		} `json:"body"`
		Links struct {
			WebUI string `json:"webui"`
		} `json:"_links"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return &Comment{
		ID:     result.ID,
		Kind:   "inline",
		Body:   result.Body.Storage.Value,
		WebURL: c.absWebURL(result.Links.WebUI),
	}, nil
}

func (c *client) AddLabel(ctx context.Context, pageID, label string) (*Label, error) {
	payload := []map[string]string{
		{"prefix": "global", "name": label},
	}

	resp, err := c.doJSON(ctx, http.MethodPost, fmt.Sprintf("/wiki/rest/api/content/%s/label", pageID), nil, payload)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrPageNotFound
		}
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Results []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Prefix string `json:"prefix"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	if len(result.Results) == 0 {
		return nil, fmt.Errorf("%w: label add returned no results", ErrAPIError)
	}

	// Prefer the result matching the requested label; fall back to the first.
	chosen := result.Results[0]
	for _, r := range result.Results {
		if r.Name == label {
			chosen = r
			break
		}
	}
	return &Label{
		ID:     chosen.ID,
		Name:   chosen.Name,
		Prefix: chosen.Prefix,
	}, nil
}

func (c *client) RemoveLabel(ctx context.Context, pageID, label string) error {
	query := url.Values{}
	query.Set("name", label)

	resp, err := c.doRequest(ctx, http.MethodDelete, fmt.Sprintf("/wiki/rest/api/content/%s/label", pageID), query)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrPageNotFound
		}
		return err
	}
	_ = resp.Body.Close()
	return nil
}

func (c *client) UploadAttachment(ctx context.Context, pageID, filePath string) (*Attachment, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	path := fmt.Sprintf("/wiki/rest/api/content/%s/child/attachment", pageID)
	resp, err := c.doMultipart(ctx, http.MethodPut, path, nil, func(mw *multipart.Writer) error {
		fw, err := mw.CreateFormFile("file", filepath.Base(filePath))
		if err != nil {
			return err
		}
		if _, err := io.Copy(fw, f); err != nil {
			return err
		}
		return mw.WriteField("minorEdit", "true")
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrPageNotFound
		}
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Results []struct {
			ID         string `json:"id"`
			Title      string `json:"title"`
			Extensions struct {
				MediaType string `json:"mediaType"`
			} `json:"extensions"`
			Metadata struct {
				MediaType string `json:"mediaType"`
			} `json:"metadata"`
			Links struct {
				Download string `json:"download"`
			} `json:"_links"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	if len(result.Results) == 0 {
		return nil, fmt.Errorf("%w: attachment upload returned no results", ErrAPIError)
	}

	r := result.Results[0]
	mediaType := r.Extensions.MediaType
	if mediaType == "" {
		mediaType = r.Metadata.MediaType
	}
	return &Attachment{
		ID:          r.ID,
		Title:       r.Title,
		MediaType:   mediaType,
		DownloadURL: r.Links.Download,
	}, nil
}
