package confluence

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
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

// unarchiveMutation restores archived pages. The operation is named because
// the gateway reports an unnamed operation as a top-level error, and the page
// id travels in variables, never interpolated into the query.
const unarchiveMutation = `mutation UnarchivePage($ids: [Long]!, $includeChildren: [Boolean]!){ bulkUnarchivePages(pageIDs: $ids, includeChildren: $includeChildren){ status taskId } }`

// UnarchivePage restores one archived page and returns the id of the long task
// doing the work. Poll it with GetLongTask.
//
// No REST endpoint unarchives a page. This calls the bulkUnarchivePages
// mutation on the Atlassian GraphQL gateway (/gateway/api/graphql), which
// accepts API-token basic auth. includeChildren needs exactly one entry per
// page id or the server fails the request. The mutation rejects a page that is
// not archived, so callers preflight with GetPage.
//
// Transport failures and non-2xx statuses keep their StatusError/sentinel
// classification. Only an HTTP 200 that reports a logical failure or an
// unrecognized shape yields ErrUnarchiveFailed.
func (c *client) UnarchivePage(ctx context.Context, pageID string) (string, error) {
	id, err := strconv.ParseInt(pageID, 10, 64)
	if err != nil {
		return "", fmt.Errorf("%w: page id %q is not numeric", ErrAPIError, pageID)
	}
	payload := map[string]any{
		"operationName": "UnarchivePage",
		"query":         unarchiveMutation,
		"variables": map[string]any{
			"ids":             []int64{id},
			"includeChildren": []bool{false},
		},
	}
	resp, err := c.doJSON(ctx, http.MethodPost, "/gateway/api/graphql", nil, payload)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		// A transport failure while reading the 200 body keeps its own
		// classification; it is not a logical failure.
		return "", err
	}
	return parseUnarchiveResponse(body)
}

// parseUnarchiveResponse strictly validates the GraphQL envelope. It fails
// closed: the submit counts only when there are no top-level errors, the
// payload is present, status is explicitly true, and a task id came back.
func parseUnarchiveResponse(body []byte) (string, error) {
	var env struct {
		Data *struct {
			Result *struct {
				Status *bool  `json:"status"`
				TaskID string `json:"taskId"`
			} `json:"bulkUnarchivePages"`
		} `json:"data"`
		Errors []struct {
			Message    string `json:"message"`
			Extensions struct {
				Data struct {
					Errors []struct {
						Message struct {
							Key string `json:"key"`
						} `json:"message"`
					} `json:"errors"`
				} `json:"data"`
			} `json:"extensions"`
		} `json:"errors"`
	}
	// json.Unmarshal rejects trailing content after the first JSON value.
	if err := json.Unmarshal(body, &env); err != nil {
		return "", fmt.Errorf("%w: could not decode unarchive response", ErrUnarchiveFailed)
	}
	if len(env.Errors) > 0 {
		var msgs []string
		for _, e := range env.Errors {
			msg := e.Message
			// The gateway puts a generic exception in message and the actual
			// reason in extensions.data.errors[].message.key.
			for _, de := range e.Extensions.Data.Errors {
				if de.Message.Key != "" {
					msg += ": " + de.Message.Key
				}
			}
			if msg != "" {
				msgs = append(msgs, msg)
			}
		}
		return "", fmt.Errorf("%w: %s", ErrUnarchiveFailed, strings.Join(msgs, "; "))
	}
	switch {
	case env.Data == nil || env.Data.Result == nil:
		return "", fmt.Errorf("%w: response missing bulkUnarchivePages payload", ErrUnarchiveFailed)
	case env.Data.Result.Status == nil || !*env.Data.Result.Status:
		return "", fmt.Errorf("%w: mutation did not report status true", ErrUnarchiveFailed)
	case env.Data.Result.TaskID == "":
		return "", fmt.Errorf("%w: mutation returned no task id", ErrUnarchiveFailed)
	}
	return env.Data.Result.TaskID, nil
}
