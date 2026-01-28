package confluence

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

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

	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", c.authHeader)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
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
		ID       string `json:"id"`
		Title    string `json:"title"`
		ParentID string `json:"parentId"`
	} `json:"results"`
	Links struct {
		Next string `json:"next"`
	} `json:"_links"`
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
				ID:       p.ID,
				Title:    p.Title,
				ParentID: p.ParentID,
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

	return allPages, nil
}

type pageContentResponse struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Body  struct {
		Storage struct {
			Value string `json:"value"`
		} `json:"storage"`
	} `json:"body"`
	Version struct {
		Number int `json:"number"`
	} `json:"version"`
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

	return &PageContent{
		ID:      result.ID,
		Title:   result.Title,
		Body:    result.Body.Storage.Value,
		Version: result.Version.Number,
	}, nil
}

type attachmentsResponse struct {
	Results []struct {
		ID           string `json:"id"`
		Title        string `json:"title"`
		MediaType    string `json:"mediaType"`
		DownloadLink string `json:"downloadLink"`
	} `json:"results"`
}

func (c *client) GetAttachments(ctx context.Context, pageID string) ([]Attachment, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/wiki/api/v2/pages/%s/attachments", pageID), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result attachmentsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	attachments := make([]Attachment, len(result.Results))
	for i, a := range result.Results {
		attachments[i] = Attachment{
			ID:          a.ID,
			Title:       a.Title,
			MediaType:   a.MediaType,
			DownloadURL: a.DownloadLink,
		}
	}

	return attachments, nil
}

func (c *client) DownloadAttachment(ctx context.Context, attachment Attachment) (io.ReadCloser, error) {
	// Download URLs from the API are relative to /wiki, not the base URL
	downloadPath := attachment.DownloadURL
	if !strings.HasPrefix(downloadPath, "/wiki") {
		downloadPath = "/wiki" + downloadPath
	}
	u := c.baseURL + downloadPath

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", c.authHeader)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%w: status %d downloading attachment", ErrAPIError, resp.StatusCode)
	}

	return resp.Body, nil
}
