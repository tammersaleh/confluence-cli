package confluence

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// LongTask is the state of a v1 long-running task
// (GET /wiki/rest/api/longtask/{id}). A running task reports Finished=false
// and Successful=false, so Successful only means something once Finished.
type LongTask struct {
	ID         string
	Finished   bool
	Successful bool
	Status     string   // "RUNNING", "FINISH_SUCCESS", ...
	Messages   []string // messages[].translation, empty entries dropped
	Errors     []string // each errors[] entry as raw JSON (shape undocumented)
}

// ArchivePage submits one page to the v1 archive endpoint
// (POST /wiki/rest/api/content/archive, marked Experimental) and returns the id
// of the long task doing the work. Poll it with GetLongTask.
//
// One page per request: the endpoint validates the whole list and rejects all
// of it with a 400 when any id is already archived or trashed. An unknown id is
// a 500, which doCore retries, so callers preflight with GetPage. The endpoint
// archives only the named page; its children stay current and are reparented
// to the archived page's parent. It also accepts a folder id, so callers that
// mean "page" must check the kind themselves.
func (c *client) ArchivePage(ctx context.Context, pageID string) (string, error) {
	payload := map[string]any{
		"pages": []map[string]string{{"id": pageID}},
	}
	resp, err := c.doJSON(ctx, http.MethodPost, "/wiki/rest/api/content/archive", nil, payload)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}
	if result.ID == "" {
		return "", fmt.Errorf("%w: archive of page %s returned no task id", ErrAPIError, pageID)
	}
	return result.ID, nil
}

// GetLongTask fetches a long task's state. The archive response's links.status
// lacks the /wiki prefix, so the path is built from the task id instead.
func (c *client) GetLongTask(ctx context.Context, taskID string) (*LongTask, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/wiki/rest/api/longtask/%s", taskID), nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		ID         string `json:"id"`
		Finished   bool   `json:"finished"`
		Successful bool   `json:"successful"`
		Status     string `json:"status"`
		Messages   []struct {
			Translation string `json:"translation"`
		} `json:"messages"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	task := &LongTask{
		ID:         result.ID,
		Finished:   result.Finished,
		Successful: result.Successful,
		Status:     result.Status,
	}
	for _, m := range result.Messages {
		if m.Translation != "" {
			task.Messages = append(task.Messages, m.Translation)
		}
	}
	for _, e := range result.Errors {
		task.Errors = append(task.Errors, string(e))
	}
	return task, nil
}
