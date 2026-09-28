package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// recordedRequest captures what the client sent so tests can assert on it.
type recordedRequest struct {
	Method string
	Path   string
	Query  map[string][]string
	Header http.Header
	Body   string
	User   string
	Pass   string
	AuthOK bool
}

type testServer struct {
	srv      *httptest.Server
	mu       sync.Mutex
	requests []recordedRequest
	// routes maps "METHOD /path" to a fixture file; fixtureStatus overrides
	// the response code per fixture name.
	routes map[string]string
	// respondOnce overrides a route for a single request, then falls back to
	// routes (used to simulate a revoked token → 401 → retry).
	respondOnce map[string]string
}

var fixtureStatus = map[string]int{
	"error_401.json":   http.StatusUnauthorized,
	"error_403.json":   http.StatusForbidden,
	"error_422.json":   http.StatusUnprocessableEntity,
	"oauth_error.json": http.StatusUnauthorized,
	"oauth_token.json": http.StatusOK, // token endpoint is a POST but returns 200
}

// newTestServer serves fixtures from testdata, substituting __BASE__ with the
// server's own URL. Any DELETE request fails the test — this client must
// never be able to destroy content.
func newTestServer(t *testing.T, routes map[string]string) (*testServer, *Client) {
	t.Helper()
	if _, ok := routes["POST /oauth/token"]; !ok {
		routes["POST /oauth/token"] = "oauth_token.json"
	}
	ts := &testServer{routes: routes, respondOnce: map[string]string{}}
	ts.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			t.Errorf("client issued a DELETE request to %s — destructive actions are forbidden", r.URL)
			http.Error(w, "forbidden", http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		user, pass, ok := r.BasicAuth()
		ts.mu.Lock()
		ts.requests = append(ts.requests, recordedRequest{
			Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(),
			Header: r.Header.Clone(), Body: string(body),
			User: user, Pass: pass, AuthOK: ok,
		})
		ts.mu.Unlock()

		key := r.Method + " " + r.URL.Path
		ts.mu.Lock()
		fixture, ok := ts.respondOnce[key]
		if ok {
			delete(ts.respondOnce, key)
		} else {
			fixture, ok = ts.routes[key]
		}
		ts.mu.Unlock()
		if !ok {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		data, err := os.ReadFile(filepath.Join("testdata", fixture))
		if err != nil {
			t.Errorf("fixture %s: %v", fixture, err)
			http.Error(w, "missing fixture", http.StatusInternalServerError)
			return
		}
		status, found := fixtureStatus[fixture]
		if !found {
			status = http.StatusOK
			if r.Method == http.MethodPost {
				status = http.StatusCreated
			}
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(strings.ReplaceAll(string(data), "__BASE__", ts.srv.URL)))
	}))
	t.Cleanup(ts.srv.Close)

	client, err := New(ts.srv.URL, "joe", "secret", 2)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return ts, client
}

func (ts *testServer) last(t *testing.T) recordedRequest {
	t.Helper()
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.requests) == 0 {
		t.Fatal("no requests recorded")
	}
	return ts.requests[len(ts.requests)-1]
}

func (ts *testServer) setRoute(method, path, fixture string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.routes[method+" "+path] = fixture
}

func (ts *testServer) count(method, path string) int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	n := 0
	for _, r := range ts.requests {
		if r.Method == method && r.Path == path {
			n++
		}
	}
	return n
}

func sentAttributes(t *testing.T, req recordedRequest) (data map[string]any, attrs map[string]any) {
	t.Helper()
	var sent map[string]any
	if err := json.Unmarshal([]byte(req.Body), &sent); err != nil {
		t.Fatalf("parsing sent body %q: %v", req.Body, err)
	}
	data = sent["data"].(map[string]any)
	attrs = data["attributes"].(map[string]any)
	return data, attrs
}

func TestDoRejectsForbiddenMethods(t *testing.T) {
	_, client := newTestServer(t, map[string]string{})
	for _, m := range []string{http.MethodDelete, http.MethodPut, http.MethodHead, "PURGE"} {
		_, err := client.do(context.Background(), m, "/jsonapi", nil, nil)
		if err == nil || !strings.Contains(err.Error(), "not permitted") {
			t.Errorf("do must reject method %s, got err=%v", m, err)
		}
	}
}

func TestPingDiscoversBundlesAndSendsBearerToken(t *testing.T) {
	ts, client := newTestServer(t, map[string]string{
		"GET /jsonapi": "entrypoint.json",
	})
	bundles, err := client.Ping(context.Background())
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if len(bundles) != 2 || bundles[0] != "article" || bundles[1] != "page" {
		t.Errorf("bundles = %v, want [article page]", bundles)
	}
	req := ts.last(t)
	if got := req.Header.Get("Authorization"); got != "Bearer test-token-abc" {
		t.Errorf("Authorization = %q, want Bearer test-token-abc", got)
	}
	if got := req.Header.Get("Accept"); got != contentType {
		t.Errorf("Accept = %q, want %q", got, contentType)
	}
}

func TestTokenRequestShape(t *testing.T) {
	ts, client := newTestServer(t, map[string]string{
		"GET /jsonapi": "entrypoint.json",
	})
	if _, err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	ts.mu.Lock()
	var tokenReq *recordedRequest
	for i := range ts.requests {
		if ts.requests[i].Path == "/oauth/token" {
			tokenReq = &ts.requests[i]
		}
	}
	ts.mu.Unlock()
	if tokenReq == nil {
		t.Fatal("no token request made")
	}
	if got := tokenReq.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
		t.Errorf("token Content-Type = %q", got)
	}
	form, err := url.ParseQuery(tokenReq.Body)
	if err != nil {
		t.Fatalf("parsing token form body: %v", err)
	}
	if form.Get("grant_type") != "client_credentials" ||
		form.Get("client_id") != "joe" || form.Get("client_secret") != "secret" {
		t.Errorf("token form = %v", form)
	}
}

func TestTokenIsCachedAcrossRequests(t *testing.T) {
	ts, client := newTestServer(t, map[string]string{
		"GET /jsonapi":                             "entrypoint.json",
		"GET /jsonapi/filter_format/filter_format": "filter_formats.json",
	})
	if _, err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if _, err := client.ListFilterFormats(context.Background()); err != nil {
		t.Fatalf("ListFilterFormats: %v", err)
	}
	if n := ts.count(http.MethodPost, "/oauth/token"); n != 1 {
		t.Errorf("token fetched %d times, want 1 (cache miss)", n)
	}
}

func TestRevokedTokenIsRefreshedAndRetriedOnce(t *testing.T) {
	ts, client := newTestServer(t, map[string]string{
		"GET /jsonapi": "entrypoint.json",
	})
	// First API call is rejected as if the token were revoked; the retry
	// must fetch a fresh token and succeed.
	ts.mu.Lock()
	ts.respondOnce["GET /jsonapi"] = "error_401.json"
	ts.mu.Unlock()

	bundles, err := client.Ping(context.Background())
	if err != nil {
		t.Fatalf("Ping after token refresh: %v", err)
	}
	if len(bundles) != 2 {
		t.Errorf("bundles = %v", bundles)
	}
	if n := ts.count(http.MethodPost, "/oauth/token"); n != 2 {
		t.Errorf("token fetched %d times, want 2 (initial + refresh)", n)
	}
	if n := ts.count(http.MethodGet, "/jsonapi"); n != 2 {
		t.Errorf("/jsonapi hit %d times, want 2 (401 + retry)", n)
	}
}

func TestPersistent401DoesNotRetryForever(t *testing.T) {
	ts, client := newTestServer(t, map[string]string{
		"GET /jsonapi": "error_401.json",
	})
	_, err := client.Ping(context.Background())
	if !IsUnauthorized(err) {
		t.Fatalf("want 401 error, got %v", err)
	}
	if n := ts.count(http.MethodGet, "/jsonapi"); n != 2 {
		t.Errorf("/jsonapi hit %d times, want exactly 2 (one retry only)", n)
	}
}

func TestBadClientCredentials(t *testing.T) {
	_, client := newTestServer(t, map[string]string{
		"POST /oauth/token": "oauth_error.json",
	})
	_, err := client.Ping(context.Background())
	var authErr *AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("want *AuthError, got %v", err)
	}
	if authErr.Code != "invalid_client" {
		t.Errorf("Code = %q", authErr.Code)
	}
	if msg := UserMessage(err); !strings.Contains(msg, "client ID and client secret") {
		t.Errorf("UserMessage = %q", msg)
	}
}

func TestListContentTypesParsesRevisionFlag(t *testing.T) {
	_, client := newTestServer(t, map[string]string{
		"GET /jsonapi/node_type/node_type": "node_types.json",
	})
	types, err := client.ListContentTypes(context.Background())
	if err != nil {
		t.Fatalf("ListContentTypes: %v", err)
	}
	if len(types) != 2 {
		t.Fatalf("got %d types, want 2", len(types))
	}
	article := types[0] // sorted by name: Article, Basic page
	if article.Machine != "article" || !article.NewRevision || !article.Verified || !article.Editable() {
		t.Errorf("article: %+v — want editable", article)
	}
	page := types[1]
	if page.Machine != "page" || page.NewRevision || page.Editable() {
		t.Errorf("page: %+v — want read-only (revisions disabled)", page)
	}
	if !strings.Contains(page.ReadOnlyReason(), "revisions disabled") {
		t.Errorf("ReadOnlyReason = %q", page.ReadOnlyReason())
	}
	unverified := ContentType{Machine: "article", NewRevision: true, Verified: false}
	if unverified.Editable() {
		t.Error("unverified content type must not be editable")
	}
	if !strings.Contains(unverified.ReadOnlyReason(), "unverified") {
		t.Errorf("unverified reason = %q", unverified.ReadOnlyReason())
	}
}

func TestListContentTypesForbiddenReturnsSentinel(t *testing.T) {
	_, client := newTestServer(t, map[string]string{
		"GET /jsonapi/node_type/node_type": "error_403.json",
	})
	_, err := client.ListContentTypes(context.Background())
	if !errors.Is(err, ErrRevisionCheckForbidden) {
		t.Fatalf("err = %v, want ErrRevisionCheckForbidden", err)
	}
}

func TestListNodesQueryConstruction(t *testing.T) {
	ts, client := newTestServer(t, map[string]string{
		"GET /jsonapi/node/article": "nodes_page1.json",
	})
	page, err := client.ListNodes(context.Background(), "article", ListOpts{TitleContains: "hello world"})
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	q := ts.last(t).Query
	checks := map[string]string{
		"page[limit]":                    "2",
		"sort":                           "-changed",
		"fields[node--article]":          "title,status,changed",
		"filter[t][condition][path]":     "title",
		"filter[t][condition][operator]": "CONTAINS",
		"filter[t][condition][value]":    "hello world",
	}
	for key, want := range checks {
		if got := q[key]; len(got) != 1 || got[0] != want {
			t.Errorf("query %s = %v, want %q", key, got, want)
		}
	}
	if len(page.Nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(page.Nodes))
	}
	n := page.Nodes[0]
	if n.Title != "First article" || !n.Status || n.UUID != "aaaaaaaa-0001-0000-0000-000000000000" {
		t.Errorf("node[0] = %+v", n)
	}
	if n.Changed.IsZero() {
		t.Error("changed date not parsed")
	}
	if n.HasBody {
		t.Error("sparse list fieldset should not report HasBody")
	}
	if page.Nodes[1].Status {
		t.Error("node[1] should be unpublished")
	}
}

func TestListNodesFollowsNextLinkVerbatim(t *testing.T) {
	ts, client := newTestServer(t, map[string]string{
		"GET /jsonapi/node/article": "nodes_page1.json",
	})
	page1, err := client.ListNodes(context.Background(), "article", ListOpts{})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if page1.Next == "" {
		t.Fatal("expected next link on page 1")
	}
	if !strings.HasPrefix(page1.Next, ts.srv.URL) {
		t.Fatalf("next link %q not rewritten to test server", page1.Next)
	}
	ts.setRoute("GET", "/jsonapi/node/article", "nodes_page2.json")
	page2, err := client.ListNodes(context.Background(), "article", ListOpts{NextURL: page1.Next})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	req := ts.last(t)
	if got := req.Query["page[offset]"]; len(got) != 1 || got[0] != "2" {
		t.Errorf("next URL not used verbatim; query = %v", req.Query)
	}
	if len(page2.Nodes) != 1 || page2.Nodes[0].Title != "Third article" {
		t.Errorf("page2 nodes = %+v", page2.Nodes)
	}
	if page2.Next != "" {
		t.Errorf("last page must have empty Next, got %q", page2.Next)
	}
}

func TestGetNodeBodyPresence(t *testing.T) {
	_, client := newTestServer(t, map[string]string{
		"GET /jsonapi/node/article/aaaaaaaa-0001-0000-0000-000000000000": "node_single.json",
		"GET /jsonapi/node/page/bbbbbbbb-0001-0000-0000-000000000000":    "node_single_nobody.json",
	})
	withBody, err := client.GetNode(context.Background(), "article", "aaaaaaaa-0001-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if !withBody.HasBody || !strings.Contains(withBody.Body, "**markdown**") || withBody.BodyFormat != "markdown_easy" {
		t.Errorf("body node = %+v", withBody)
	}
	noBody, err := client.GetNode(context.Background(), "page", "bbbbbbbb-0001-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("GetNode nobody: %v", err)
	}
	if noBody.HasBody {
		t.Errorf("node without body attribute must have HasBody=false: %+v", noBody)
	}
}

func TestCreateNodePayloadShape(t *testing.T) {
	ts, client := newTestServer(t, map[string]string{
		"POST /jsonapi/node/article": "node_single.json",
	})
	body := "# Hello\n\nSome **markdown** text."
	unpublished := false
	created, err := client.CreateNode(context.Background(), "article", NodeInput{
		Title:       "First article",
		Body:        &body,
		BodyFormat:  "markdown_easy",
		Status:      &unpublished,
		RevisionLog: "Created via drupaltui",
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	if created.UUID == "" {
		t.Error("created node UUID not parsed")
	}
	req := ts.last(t)
	if got := req.Header.Get("Content-Type"); got != contentType {
		t.Errorf("Content-Type = %q, want %q", got, contentType)
	}
	data, attrs := sentAttributes(t, req)
	if data["type"] != "node--article" {
		t.Errorf("data.type = %v", data["type"])
	}
	if _, hasID := data["id"]; hasID {
		t.Error("POST must not include data.id")
	}
	if attrs["title"] != "First article" {
		t.Errorf("title = %v", attrs["title"])
	}
	if attrs["status"] != false {
		t.Errorf("status = %v, want false (unpublished by default)", attrs["status"])
	}
	if attrs["revision_log"] != "Created via drupaltui" {
		t.Errorf("revision_log = %v", attrs["revision_log"])
	}
	b := attrs["body"].(map[string]any)
	if b["value"] != body || b["format"] != "markdown_easy" {
		t.Errorf("body = %v", b)
	}
}

func TestCreateNodeOmitsNilFields(t *testing.T) {
	ts, client := newTestServer(t, map[string]string{
		"POST /jsonapi/node/page": "node_single_nobody.json",
	})
	if _, err := client.CreateNode(context.Background(), "page", NodeInput{Title: "Title only"}); err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	_, attrs := sentAttributes(t, ts.last(t))
	if _, has := attrs["body"]; has {
		t.Error("nil Body must be omitted from attributes")
	}
	if _, has := attrs["status"]; has {
		t.Error("nil Status must be omitted from attributes")
	}
}

func TestUpdateNodeIncludesID(t *testing.T) {
	uuid := "aaaaaaaa-0001-0000-0000-000000000000"
	ts, client := newTestServer(t, map[string]string{
		"PATCH /jsonapi/node/article/" + uuid: "node_single.json",
	})
	if _, err := client.UpdateNode(context.Background(), "article", uuid, NodeInput{Title: "Renamed"}); err != nil {
		t.Fatalf("UpdateNode: %v", err)
	}
	data, _ := sentAttributes(t, ts.last(t))
	if data["id"] != uuid {
		t.Errorf("PATCH data.id = %v, want %s", data["id"], uuid)
	}
}

func TestSetStatusSendsMinimalPayload(t *testing.T) {
	uuid := "aaaaaaaa-0001-0000-0000-000000000000"
	ts, client := newTestServer(t, map[string]string{
		"PATCH /jsonapi/node/article/" + uuid: "node_single.json",
	})
	if _, err := client.SetStatus(context.Background(), "article", uuid, true, "Published via drupaltui"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	_, attrs := sentAttributes(t, ts.last(t))
	if len(attrs) != 2 || attrs["status"] != true || attrs["revision_log"] != "Published via drupaltui" {
		t.Errorf("attributes = %v, want only status + revision_log", attrs)
	}
}

func TestListFilterFormats(t *testing.T) {
	_, client := newTestServer(t, map[string]string{
		"GET /jsonapi/filter_format/filter_format": "filter_formats.json",
	})
	formats, err := client.ListFilterFormats(context.Background())
	if err != nil {
		t.Fatalf("ListFilterFormats: %v", err)
	}
	if len(formats) != 2 || formats[1].Machine != "markdown_easy" || formats[1].Name != "Markdown" {
		t.Errorf("formats = %+v", formats)
	}
}

func TestErrorMessages(t *testing.T) {
	uuid := "aaaaaaaa-0001-0000-0000-000000000000"
	_, client := newTestServer(t, map[string]string{
		"PATCH /jsonapi/node/article/" + uuid: "error_422.json",
	})
	_, err := client.UpdateNode(context.Background(), "article", uuid, NodeInput{Title: ""})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *APIError, got %v", err)
	}
	if apiErr.StatusCode != 422 {
		t.Errorf("StatusCode = %d", apiErr.StatusCode)
	}
	if msg := apiErr.UserMessage(); !strings.Contains(msg, "This value should not be null") {
		t.Errorf("422 UserMessage should surface Drupal detail, got %q", msg)
	}

	cases := []struct {
		err  *APIError
		want string
	}{
		{&APIError{StatusCode: 401, Method: http.MethodGet}, "client ID and secret"},
		{&APIError{StatusCode: 405, Method: http.MethodPost}, "read-only"},
		{&APIError{StatusCode: 403, Method: http.MethodPatch}, "Write blocked"},
		{&APIError{StatusCode: 403, Method: http.MethodGet}, "Permission denied"},
		{&APIError{StatusCode: 404, Method: http.MethodGet}, "Not found"},
	}
	for _, c := range cases {
		if msg := c.err.UserMessage(); !strings.Contains(msg, c.want) {
			t.Errorf("%d %s UserMessage = %q, want it to contain %q",
				c.err.StatusCode, c.err.Method, msg, c.want)
		}
	}
}
