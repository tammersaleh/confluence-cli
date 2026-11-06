package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/tammersaleh/confluence-cli/internal/output"
)

const testMe = "712020:me"

// restrictionState is the in-memory restriction store behind restrictionServer.
// Lists are keyed by operation; the server renders them in the v1 shape.
type restrictionState struct {
	readUsers, readGroups, updateUsers, updateGroups []string
	puts                                             []map[string]any // decoded PUT payloads, in order
	deletes                                          int
	calls                                            []string // "METHOD path" in order
	putStatus                                        int      // non-zero forces this status on PUT
	putMessage                                       string
}

func renderRestrictions(st *restrictionState) string {
	users := func(ids []string) map[string]any {
		rs := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			rs = append(rs, map[string]any{"type": "known", "accountId": id, "displayName": "Name " + id})
		}
		return map[string]any{"results": rs, "start": 0, "limit": 200, "size": len(rs)}
	}
	groups := func(names []string) map[string]any {
		rs := make([]map[string]any, 0, len(names))
		for _, n := range names {
			rs = append(rs, map[string]any{"type": "group", "name": n, "id": "gid-" + n})
		}
		return map[string]any{"results": rs, "start": 0, "limit": 200, "size": len(rs)}
	}
	b, _ := json.Marshal(map[string]any{
		"results": []map[string]any{
			{"operation": "read", "restrictions": map[string]any{"user": users(st.readUsers), "group": groups(st.readGroups)}},
			{"operation": "update", "restrictions": map[string]any{"user": users(st.updateUsers), "group": groups(st.updateGroups)}},
		},
		"start": 0, "limit": 200, "size": 2,
	})
	return string(b)
}

// restrictionHandler serves /wiki/rest/api/user/current and the v1 restriction
// endpoint for page 123, applying PUT payloads to st.
func restrictionHandler(t *testing.T, st *restrictionState) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		st.calls = append(st.calls, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/wiki/rest/api/user/current":
			_, _ = io.WriteString(w, `{"accountId":"`+testMe+`","displayName":"Me","email":"me@example.com"}`)
		case "/wiki/rest/api/content/123/restriction":
			switch r.Method {
			case http.MethodGet:
				_, _ = io.WriteString(w, renderRestrictions(st))
			case http.MethodPut:
				var payload []map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Errorf("decode PUT: %v", err)
				}
				st.puts = append(st.puts, map[string]any{"ops": payload})
				if st.putStatus != 0 {
					w.WriteHeader(st.putStatus)
					_, _ = io.WriteString(w, `{"statusCode":`+strconv.Itoa(st.putStatus)+`,"message":"`+st.putMessage+`"}`)
					return
				}
				applyPut(st, payload)
				_, _ = io.WriteString(w, renderRestrictions(st))
			case http.MethodDelete:
				st.deletes++
				st.readUsers, st.readGroups, st.updateUsers, st.updateGroups = nil, nil, nil, nil
				_, _ = io.WriteString(w, renderRestrictions(st))
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
		default:
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func applyPut(st *restrictionState, payload []map[string]any) {
	ids := func(list any, key string) []string {
		var out []string
		arr, _ := list.([]any)
		for _, e := range arr {
			m, _ := e.(map[string]any)
			v, _ := m[key].(string)
			out = append(out, v)
		}
		return out
	}
	for _, op := range payload {
		restr, _ := op["restrictions"].(map[string]any)
		switch op["operation"] {
		case "read":
			st.readUsers, st.readGroups = ids(restr["user"], "accountId"), ids(restr["group"], "name")
		case "update":
			st.updateUsers, st.updateGroups = ids(restr["user"], "accountId"), ids(restr["group"], "name")
		}
	}
}

func newRestrictionCLI(t *testing.T, st *restrictionState) (*CLI, *bytes.Buffer, *httptest.Server) {
	t.Helper()
	clearCredEnv(t)
	server := httptest.NewServer(restrictionHandler(t, st))
	t.Cleanup(server.Close)
	setEnvCreds(t, server.URL)
	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))
	return c, &out, server
}

// putOps returns the decoded operations of the nth PUT, keyed by operation.
func putOps(t *testing.T, st *restrictionState, n int) map[string]map[string]any {
	t.Helper()
	if len(st.puts) <= n {
		t.Fatalf("expected at least %d PUTs, got %d", n+1, len(st.puts))
	}
	ops, _ := st.puts[n]["ops"].([]map[string]any)
	out := map[string]map[string]any{}
	for _, op := range ops {
		name, _ := op["operation"].(string)
		restr, _ := op["restrictions"].(map[string]any)
		out[name] = restr
	}
	return out
}

func TestPageRestrictionsGet_Rows(t *testing.T) {
	st := &restrictionState{readUsers: []string{"u1"}, readGroups: []string{"g1"}}
	c, out, _ := newRestrictionCLI(t, st)

	cmd := &PageRestrictionsGetCmd{Page: "123"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 3 {
		t.Fatalf("expected 2 rows + meta, got %d: %q", len(lines), out.String())
	}
	read := lines[0]
	if read["page_id"] != "123" || read["operation"] != "read" {
		t.Errorf("read row: %v", read)
	}
	users, _ := read["users"].([]any)
	if len(users) != 1 {
		t.Fatalf("read users: %v", read["users"])
	}
	u, _ := users[0].(map[string]any)
	if u["account_id"] != "u1" || u["display_name"] != "Name u1" {
		t.Errorf("read user: %v", u)
	}
	groups, _ := read["groups"].([]any)
	if len(groups) != 1 {
		t.Fatalf("read groups: %v", read["groups"])
	}
	g, _ := groups[0].(map[string]any)
	if g["name"] != "g1" || g["id"] != "gid-g1" {
		t.Errorf("read group: %v", g)
	}

	update := lines[1]
	if update["operation"] != "update" {
		t.Errorf("update row: %v", update)
	}
	// Empty lists are emitted as [] rather than omitted or null.
	if us, ok := update["users"].([]any); !ok || len(us) != 0 {
		t.Errorf("update users should be empty array: %v", update["users"])
	}
	if gs, ok := update["groups"].([]any); !ok || len(gs) != 0 {
		t.Errorf("update groups should be empty array: %v", update["groups"])
	}
	if _, ok := lines[2]["_meta"].(map[string]any); !ok {
		t.Errorf("last line not meta: %v", lines[2])
	}
}

func TestPageRestrictionsGet_NotFound(t *testing.T) {
	clearCredEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	err := (&PageRestrictionsGetCmd{Page: "123"}).Run(c)
	var oErr *output.Error
	if !errors.As(err, &oErr) || oErr.Err != "page_not_found" {
		t.Fatalf("expected page_not_found, got %T: %v", err, err)
	}
}

func TestPageRestrictionsSet_OnlyMe(t *testing.T) {
	st := &restrictionState{readUsers: []string{"other"}, updateGroups: []string{"editors"}}
	c, out, _ := newRestrictionCLI(t, st)

	cmd := &PageRestrictionsSetCmd{Page: "123", OnlyMe: true}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	ops := putOps(t, st, 0)
	for _, op := range []string{"read", "update"} {
		users, _ := ops[op]["user"].([]any)
		groups, _ := ops[op]["group"].([]any)
		if len(users) != 1 || users[0].(map[string]any)["accountId"] != testMe {
			t.Errorf("%s users = %v, want only me", op, ops[op]["user"])
		}
		if groups == nil || len(groups) != 0 {
			t.Errorf("%s groups = %v, want empty array", op, ops[op]["group"])
		}
	}
	if st.calls[0] != "GET /wiki/rest/api/user/current" {
		t.Errorf("first call = %q, want user/current", st.calls[0])
	}

	// Output is the final state, read back after the PUT.
	lines := parseLines(t, out.String())
	if len(lines) != 3 {
		t.Fatalf("expected 2 rows + meta, got %d: %q", len(lines), out.String())
	}
	read := lines[0]
	users, _ := read["users"].([]any)
	if read["operation"] != "read" || len(users) != 1 || users[0].(map[string]any)["account_id"] != testMe {
		t.Errorf("read row: %v", read)
	}
	update := lines[1]
	ugroups, _ := update["groups"].([]any)
	if update["operation"] != "update" || len(ugroups) != 0 {
		t.Errorf("update row should have lost the editors group: %v", update)
	}
}

func TestPageRestrictionsSet_Flags(t *testing.T) {
	st := &restrictionState{}
	c, _, _ := newRestrictionCLI(t, st)

	cmd := &PageRestrictionsSetCmd{
		Page:         "123",
		ReadUsers:    []string{"u1", "u2"},
		ReadGroups:   []string{"g1"},
		UpdateUsers:  []string{"u1"},
		UpdateGroups: nil,
	}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	// No user lookup is needed when the caller names principals explicitly.
	for _, call := range st.calls {
		if call == "GET /wiki/rest/api/user/current" {
			t.Errorf("unexpected user/current lookup: %v", st.calls)
		}
	}
	if len(st.readUsers) != 2 || st.readUsers[0] != "u1" || st.readUsers[1] != "u2" {
		t.Errorf("read users = %v", st.readUsers)
	}
	if len(st.readGroups) != 1 || st.readGroups[0] != "g1" {
		t.Errorf("read groups = %v", st.readGroups)
	}
	if len(st.updateUsers) != 1 || st.updateUsers[0] != "u1" || len(st.updateGroups) != 0 {
		t.Errorf("update = users %v groups %v", st.updateUsers, st.updateGroups)
	}
	ops := putOps(t, st, 0)
	if g, ok := ops["update"]["group"].([]any); !ok || len(g) != 0 {
		t.Errorf("update group must be sent as an empty array: %v", ops["update"]["group"])
	}
}

func TestPageRestrictionsSet_UpdateOnlyClearsRead(t *testing.T) {
	st := &restrictionState{readUsers: []string{"old"}}
	c, _, _ := newRestrictionCLI(t, st)

	cmd := &PageRestrictionsSetCmd{Page: "123", UpdateUsers: []string{"u1"}}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(st.readUsers) != 0 {
		t.Errorf("read users should be cleared by a declarative set: %v", st.readUsers)
	}
	if len(st.updateUsers) != 1 || st.updateUsers[0] != "u1" {
		t.Errorf("update users = %v", st.updateUsers)
	}
}

func TestPageRestrictionsSet_NoFlags(t *testing.T) {
	st := &restrictionState{}
	c, _, _ := newRestrictionCLI(t, st)

	err := (&PageRestrictionsSetCmd{Page: "123"}).Run(c)
	var oErr *output.Error
	if !errors.As(err, &oErr) || oErr.Err != "invalid_input" || oErr.Code != output.ExitGeneral {
		t.Fatalf("expected invalid_input, got %T: %v", err, err)
	}
	if len(st.calls) != 0 {
		t.Errorf("no requests expected, got %v", st.calls)
	}
}

func TestPageRestrictionsSet_OnlyMeConflict(t *testing.T) {
	st := &restrictionState{}
	c, _, _ := newRestrictionCLI(t, st)

	err := (&PageRestrictionsSetCmd{Page: "123", OnlyMe: true, ReadUsers: []string{"u1"}}).Run(c)
	var oErr *output.Error
	if !errors.As(err, &oErr) || oErr.Err != "invalid_input" {
		t.Fatalf("expected invalid_input, got %T: %v", err, err)
	}
	if len(st.calls) != 0 {
		t.Errorf("no requests expected, got %v", st.calls)
	}
}

func TestPageRestrictionsSet_SelfLockout(t *testing.T) {
	st := &restrictionState{
		putStatus:  http.StatusBadRequest,
		putMessage: "Provided ContentRestrictions evicts current user (you) from: [update]. Must include yourself.",
	}
	c, out, _ := newRestrictionCLI(t, st)

	err := (&PageRestrictionsSetCmd{Page: "123", ReadUsers: []string{"u1"}}).Run(c)
	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "self_lockout" || oErr.Code != output.ExitGeneral || oErr.Hint == "" {
		t.Errorf("got Err=%q Code=%d Hint=%q", oErr.Err, oErr.Code, oErr.Hint)
	}
	if out.Len() != 0 {
		t.Errorf("nothing should be written to stdout on a fatal error: %q", out.String())
	}
}

func TestPageRestrictionsClear(t *testing.T) {
	st := &restrictionState{readUsers: []string{"u1"}}
	c, out, _ := newRestrictionCLI(t, st)

	if err := (&PageRestrictionsClearCmd{Page: "123"}).Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if st.deletes != 1 {
		t.Errorf("deletes = %d, want 1", st.deletes)
	}
	lines := parseLines(t, out.String())
	if len(lines) != 2 {
		t.Fatalf("expected 1 row + meta, got %d: %q", len(lines), out.String())
	}
	if lines[0]["page_id"] != "123" || lines[0]["cleared"] != true {
		t.Errorf("row: %v", lines[0])
	}
}

func TestPageRestrictionsClear_NotFound(t *testing.T) {
	clearCredEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	err := (&PageRestrictionsClearCmd{Page: "123"}).Run(c)
	var oErr *output.Error
	if !errors.As(err, &oErr) || oErr.Err != "page_not_found" {
		t.Fatalf("expected page_not_found, got %T: %v", err, err)
	}
}
