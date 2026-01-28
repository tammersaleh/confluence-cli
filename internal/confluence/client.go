package confluence

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	maxRetries     = 5
	baseRetryDelay = 500 * time.Millisecond
	maxRetryDelay  = 30 * time.Second
)

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
	jitter := time.Duration(rand.Int63n(int64(delay) / 2))
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

func (c *client) doRequest(ctx context.Context, method, path string, query url.Values) (*http.Response, error) {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := retryDelay(attempt - 1)
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

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		if isRetryable(resp.StatusCode) {
			resp.Body.Close()
			lastErr = fmt.Errorf("%w: status %d", ErrAPIError, resp.StatusCode)
			continue
		}

		switch resp.StatusCode {
		case http.StatusUnauthorized:
			resp.Body.Close()
			return nil, ErrUnauthorized
		case http.StatusForbidden:
			resp.Body.Close()
			return nil, ErrForbidden
		case http.StatusNotFound:
			resp.Body.Close()
			return nil, ErrSpaceNotFound
		}

		if resp.StatusCode >= 400 {
			resp.Body.Close()
			return nil, fmt.Errorf("%w: status %d", ErrAPIError, resp.StatusCode)
		}

		return resp, nil
	}

	return nil, fmt.Errorf("after %d retries: %w", maxRetries, lastErr)
}

type spacesResponse struct {
	Results []struct {
		ID   string `json:"id"`
		Key  string `json:"key"`
		Name string `json:"name"`
	} `json:"results"`
}

func (c *client) GetSpace(ctx context.Context, spaceKey string) (*Space, error) {
	query := url.Values{}
	query.Set("keys", spaceKey)

	resp, err := c.doRequest(ctx, http.MethodGet, "/wiki/api/v2/spaces", query)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result spacesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	if len(result.Results) == 0 {
		return nil, ErrSpaceNotFound
	}

	s := result.Results[0]
	return &Space{
		ID:   s.ID,
		Key:  s.Key,
		Name: s.Name,
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
	ID       string `json:"id"`
	ParentID string `json:"parentId"`
}

func (c *client) GetPages(ctx context.Context, spaceID string) ([]Page, error) {
	var allPages []Page
	path := fmt.Sprintf("/wiki/api/v2/spaces/%s/pages", spaceID)
	query := url.Values{}
	query.Set("limit", "250")

	for {
		resp, err := c.doRequest(ctx, http.MethodGet, path, query)
		if err != nil {
			return nil, err
		}

		var result pagesResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decoding response: %w", err)
		}
		resp.Body.Close()

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
		parentID, err := c.getPageParentID(ctx, allPages[i].ID)
		if err != nil {
			return nil, fmt.Errorf("getting parent for page %s: %w", allPages[i].ID, err)
		}
		allPages[i].ParentID = parentID
	}

	return allPages, nil
}

func (c *client) getPageParentID(ctx context.Context, pageID string) (string, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/wiki/api/v2/pages/%s", pageID), nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result pageResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}

	return result.ParentID, nil
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
		return nil, err
	}
	defer resp.Body.Close()

	var result pageContentResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	webURL := result.Links.WebUI
	if webURL != "" && !strings.HasPrefix(webURL, "http") {
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
			return nil, err
		}

		var result attachmentsResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decoding response: %w", err)
		}
		resp.Body.Close()

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

func (c *client) DownloadAttachment(ctx context.Context, attachment Attachment) (io.ReadCloser, error) {
	// Download URLs from the API are relative to /wiki, not the base URL
	downloadPath := attachment.DownloadURL
	if !strings.HasPrefix(downloadPath, "/wiki") {
		downloadPath = "/wiki" + downloadPath
	}
	u := c.baseURL + downloadPath

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := retryDelay(attempt - 1)
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

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		if isRetryable(resp.StatusCode) {
			resp.Body.Close()
			lastErr = fmt.Errorf("%w: status %d downloading %s", ErrAPIError, resp.StatusCode, u)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("%w: status %d downloading %s", ErrAPIError, resp.StatusCode, u)
		}

		return resp.Body, nil
	}

	return nil, fmt.Errorf("after %d retries: %w", maxRetries, lastErr)
}
