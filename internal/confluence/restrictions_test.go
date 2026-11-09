package confluence

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// restrictionPage renders one v1 restriction response page. Every nested list
// reports the same start/limit; size is the length of its results.
func restrictionPage(start, limit int, readUsers, readGroups, updateUsers, updateGroups []string) string {
	users := func(ids []string) map[string]any {
		rs := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			rs = append(rs, map[string]any{"type": "known", "accountId": id, "displayName": "Name " + id})
		}
		return map[string]any{"results": rs, "start": start, "limit": limit, "size": len(rs)}
	}
	groups := func(names []string) map[string]any {
		rs := make([]map[string]any, 0, len(names))
		for _, n := range names {
			rs = append(rs, map[string]any{"type": "group", "name": n, "id": "gid-" + n})
		}
		return map[string]any{"results": rs, "start": start, "limit": limit, "size": len(rs)}
	}
	body := map[string]any{
		"results": []map[string]any{
			{"operation": "read", "restrictions": map[string]any{"user": users(readUsers), "group": groups(readGroups)}},
			{"operation": "update", "restrictions": map[string]any{"user": users(updateUsers), "group": groups(updateGroups)}},
		},
		"start": start, "limit": limit, "size": 2,
	}
	b, _ := json.Marshal(body)
	return string(b)
}

func TestClient_GetRestrictions_DrainsNestedPages(t *testing.T) {
	var starts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/rest/api/content/123/restriction" || r.Method != http.MethodGet {
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		q := r.URL.Query()
		if q.Get("expand") != "restrictions.user,restrictions.group" {
			t.Errorf("expand = %q", q.Get("expand"))
		}
		if q.Get("limit") == "" {
			t.Errorf("limit not sent")
		}
		starts = append(starts, q.Get("start"))
		// The server honors a limit of 1 regardless of what was requested, so
		// the drain must follow the response's limit, not the request's.
		switch q.Get("start") {
		case "", "0":
			_, _ = io.WriteString(w, restrictionPage(0, 1, []string{"u1"}, []string{"g1"}, []string{"u2"}, nil))
		case "1":
			_, _ = io.WriteString(w, restrictionPage(1, 1, []string{"u3"}, nil, nil, nil))
		default:
			_, _ = io.WriteString(w, restrictionPage(2, 1, nil, nil, nil, nil))
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	ops, err := c.GetRestrictions(context.Background(), "123")
	if err != nil {
		t.Fatalf("GetRestrictions: %v", err)
	}
	if len(starts) != 3 || starts[1] != "1" || starts[2] != "2" {
		t.Errorf("start sequence = %v, want [0 1 2]", starts)
	}
	if len(ops) != 2 || ops[0].Operation != RestrictionRead || ops[1].Operation != RestrictionUpdate {
		t.Fatalf("ops = %+v", ops)
	}
	read, update := ops[0], ops[1]
	if len(read.Users) != 2 || read.Users[0].AccountID != "u1" || read.Users[0].DisplayName != "Name u1" || read.Users[1].AccountID != "u3" {
		t.Errorf("read users = %+v", read.Users)
	}
	if len(read.Groups) != 1 || read.Groups[0].Name != "g1" || read.Groups[0].ID != "gid-g1" {
		t.Errorf("read groups = %+v", read.Groups)
	}
	if len(update.Users) != 1 || update.Users[0].AccountID != "u2" || len(update.Groups) != 0 {
		t.Errorf("update = %+v", update)
	}
}

func TestClient_GetRestrictions_SinglePageStops(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.WriteString(w, restrictionPage(0, 200, []string{"u1"}, nil, nil, nil))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	ops, err := c.GetRestrictions(context.Background(), "123")
	if err != nil {
		t.Fatalf("GetRestrictions: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
	// Both operations are always present, even when the update lists are empty.
	if len(ops) != 2 || len(ops[1].Users) != 0 || ops[1].Users == nil || ops[1].Groups == nil {
		t.Errorf("ops = %+v (slices must be non-nil)", ops)
	}
}

func TestClient_GetRestrictions_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"statusCode":404,"message":"No content with id <ContentId{id=123}> can be found"}`)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	_, err := c.GetRestrictions(context.Background(), "123")
	if !errors.Is(err, ErrPageNotFound) {
		t.Errorf("err = %v, want ErrPageNotFound", err)
	}
}

func TestClient_SetRestrictions_Payload(t *testing.T) {
	var got []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/rest/api/content/123/restriction" || r.Method != http.MethodPut {
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode: %v", err)
		}
		_, _ = io.WriteString(w, restrictionPage(0, 200, []string{"u1"}, []string{"g1"}, []string{"u1"}, nil))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	err := c.SetRestrictions(context.Background(), "123", []OperationRestriction{
		{Operation: RestrictionRead, Users: []RestrictionUser{{AccountID: "u1"}}, Groups: []RestrictionGroup{{Name: "g1"}}},
		{Operation: RestrictionUpdate, Users: []RestrictionUser{{AccountID: "u1"}}},
	})
	if err != nil {
		t.Fatalf("SetRestrictions: %v", err)
	}

	want := `[{"operation":"read","restrictions":{"group":[{"name":"g1","type":"group"}],"user":[{"accountId":"u1","type":"known"}]}},{"operation":"update","restrictions":{"group":[],"user":[{"accountId":"u1","type":"known"}]}}]`
	gotJSON, _ := json.Marshal(got)
	if string(gotJSON) != want {
		t.Errorf("payload:\n got %s\nwant %s", gotJSON, want)
	}
}

func TestClient_SetRestrictions_SelfLockout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"statusCode":400,"message":"com.atlassian.confluence.api.service.exceptions.api.BadRequestException: Provided ContentRestrictions evicts current user (you) from: [update]. Must include yourself in \"user\" sections for READ and/or UPDATE when restricting those operations."}`)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	err := c.SetRestrictions(context.Background(), "123", []OperationRestriction{{Operation: RestrictionRead}})
	if !errors.Is(err, ErrSelfLockout) {
		t.Errorf("err = %v, want ErrSelfLockout", err)
	}
}

func TestClient_SetRestrictions_OtherBadRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"statusCode":400,"message":"Subject <nope> is not a valid existing group"}`)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	err := c.SetRestrictions(context.Background(), "123", []OperationRestriction{{Operation: RestrictionRead}})
	if errors.Is(err, ErrSelfLockout) {
		t.Errorf("err = %v, must not be ErrSelfLockout", err)
	}
	if !errors.Is(err, ErrAPIError) {
		t.Errorf("err = %v, want ErrAPIError", err)
	}
	var se *StatusError
	if !errors.As(err, &se) || se.Message != "Subject <nope> is not a valid existing group" {
		t.Errorf("message not preserved: %v", err)
	}
}

func TestClient_SetRestrictions_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	err := c.SetRestrictions(context.Background(), "123", []OperationRestriction{{Operation: RestrictionRead}})
	if !errors.Is(err, ErrPageNotFound) {
		t.Errorf("err = %v, want ErrPageNotFound", err)
	}
}

func TestClient_ClearRestrictions(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/rest/api/content/123/restriction" || r.Method != http.MethodDelete {
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		called = true
		_, _ = io.WriteString(w, restrictionPage(0, 200, nil, nil, nil, nil))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	if err := c.ClearRestrictions(context.Background(), "123"); err != nil {
		t.Fatalf("ClearRestrictions: %v", err)
	}
	if !called {
		t.Error("DELETE not called")
	}
}

func TestClient_ClearRestrictions_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test@example.com", "api-token")
	if err := c.ClearRestrictions(context.Background(), "123"); !errors.Is(err, ErrPageNotFound) {
		t.Errorf("err = %v, want ErrPageNotFound", err)
	}
}
