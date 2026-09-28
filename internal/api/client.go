// Package api is a minimal Drupal JSON:API client for drupaltui.
//
// It is intentionally non-destructive: the request helper accepts only GET,
// POST, and PATCH, and no delete capability exists anywhere in the package.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const contentType = "application/vnd.api+json"

type Client struct {
	base         *url.URL
	clientID     string
	clientSecret string
	http         *http.Client
	pageLimit    int

	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

// New builds a client that authenticates with the OAuth2 client_credentials
// grant against {baseURL}/oauth/token (Drupal simple_oauth).
func New(baseURL, clientID, clientSecret string, pageLimit int) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil {
		return nil, fmt.Errorf("invalid site URL %q: %w", baseURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("site URL must start with http:// or https://, got %q", baseURL)
	}
	if pageLimit <= 0 {
		pageLimit = 50
	}
	return &Client{
		base:         u,
		clientID:     clientID,
		clientSecret: clientSecret,
		http:         &http.Client{Timeout: 30 * time.Second},
		pageLimit:    pageLimit,
	}, nil
}

// do performs a JSON:API request. path may be relative to the site root or an
// absolute URL on the same host (pagination links). Only GET, POST, and PATCH
// are permitted — this client has no destructive capability by design.
//
// A 401 triggers one token refresh and retry: the cached token may have been
// expired or revoked server-side.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any) (*document, error) {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPatch:
	default:
		return nil, fmt.Errorf("method %s is not permitted by this client", method)
	}

	var target string
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		target = path
	} else {
		u := *c.base
		u.Path = strings.TrimRight(u.Path, "/") + path
		target = u.String()
	}
	if len(query) > 0 {
		sep := "?"
		if strings.Contains(target, "?") {
			sep = "&"
		}
		target += sep + query.Encode()
	}

	var payload []byte
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encoding request: %w", err)
		}
		payload = data
	}

	for attempt := 0; ; attempt++ {
		token, err := c.accessToken(ctx)
		if err != nil {
			return nil, err
		}
		doc, err := c.request(ctx, method, target, payload, token)
		if err != nil && attempt == 0 && IsUnauthorized(err) {
			c.invalidateToken()
			continue
		}
		return doc, err
	}
}

func (c *Client) request(ctx context.Context, method, target string, payload []byte, token string) (*document, error) {
	var reqBody io.Reader
	if payload != nil {
		reqBody = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", contentType)
	if payload != nil {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("can't reach %s: %w", c.base.Host, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	var doc document
	if len(data) > 0 {
		// Tolerate non-JSON error bodies (proxies, HTML error pages).
		_ = json.Unmarshal(data, &doc)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{StatusCode: resp.StatusCode, Method: method, Items: doc.Errors}
	}
	return &doc, nil
}

// Ping checks connectivity and auth against the JSON:API entry point and
// returns bundle machine names discovered from its links (fallback discovery
// for accounts that can't read node type config).
func (c *Client) Ping(ctx context.Context) ([]string, error) {
	doc, err := c.do(ctx, http.MethodGet, "/jsonapi", nil, nil)
	if err != nil {
		return nil, err
	}
	var bundles []string
	for name := range doc.Links {
		if b, ok := strings.CutPrefix(name, "node--"); ok {
			bundles = append(bundles, b)
		}
	}
	sort.Strings(bundles)
	return bundles, nil
}

// ListContentTypes reads node type config entities, including the
// new_revision setting that gates editability. A 403 is returned as
// ErrRevisionCheckForbidden so callers can fall back to Ping discovery.
func (c *Client) ListContentTypes(ctx context.Context) ([]ContentType, error) {
	doc, err := c.do(ctx, http.MethodGet, "/jsonapi/node_type/node_type", nil, nil)
	if err != nil {
		if IsForbidden(err) {
			return nil, fmt.Errorf("%w: %w", ErrRevisionCheckForbidden, err)
		}
		return nil, err
	}
	var resources []resource
	if err := json.Unmarshal(doc.Data, &resources); err != nil {
		return nil, fmt.Errorf("parsing node types: %w", err)
	}
	types := make([]ContentType, 0, len(resources))
	for _, r := range resources {
		types = append(types, ContentType{
			Machine:     r.str("drupal_internal__type"),
			Name:        r.str("name"),
			NewRevision: r.boolean("new_revision"),
			Verified:    true,
		})
	}
	sort.Slice(types, func(i, j int) bool { return types[i].Name < types[j].Name })
	return types, nil
}

type ListOpts struct {
	TitleContains string
	NextURL       string // when set, fetched verbatim (pagination)
}

// ListNodes returns one page of nodes for a bundle, newest changed first.
// The list uses a sparse fieldset; bodies are fetched by GetNode.
func (c *Client) ListNodes(ctx context.Context, bundle string, opts ListOpts) (NodePage, error) {
	var doc *document
	var err error
	if opts.NextURL != "" {
		doc, err = c.do(ctx, http.MethodGet, opts.NextURL, nil, nil)
	} else {
		q := url.Values{}
		q.Set("page[limit]", fmt.Sprint(c.pageLimit))
		q.Set("sort", "-changed")
		q.Set("fields[node--"+bundle+"]", "title,status,changed")
		if opts.TitleContains != "" {
			q.Set("filter[t][condition][path]", "title")
			q.Set("filter[t][condition][operator]", "CONTAINS")
			q.Set("filter[t][condition][value]", opts.TitleContains)
		}
		doc, err = c.do(ctx, http.MethodGet, "/jsonapi/node/"+bundle, q, nil)
	}
	if err != nil {
		return NodePage{}, err
	}
	var resources []resource
	if err := json.Unmarshal(doc.Data, &resources); err != nil {
		return NodePage{}, fmt.Errorf("parsing node list: %w", err)
	}
	page := NodePage{Next: doc.Links.href("next")}
	for _, r := range resources {
		page.Nodes = append(page.Nodes, nodeFromResource(r))
	}
	return page, nil
}

// GetNode fetches a single node with full attributes, including the body.
func (c *Client) GetNode(ctx context.Context, bundle, uuid string) (Node, error) {
	doc, err := c.do(ctx, http.MethodGet, "/jsonapi/node/"+bundle+"/"+uuid, nil, nil)
	if err != nil {
		return Node{}, err
	}
	var r resource
	if err := json.Unmarshal(doc.Data, &r); err != nil {
		return Node{}, fmt.Errorf("parsing node: %w", err)
	}
	return nodeFromResource(r), nil
}

func (in NodeInput) attributes() map[string]any {
	attrs := map[string]any{}
	if in.Title != "" {
		attrs["title"] = in.Title
	}
	if in.Body != nil {
		attrs["body"] = bodyField{Value: *in.Body, Format: in.BodyFormat}
	}
	if in.Status != nil {
		attrs["status"] = *in.Status
	}
	if in.RevisionLog != "" {
		attrs["revision_log"] = in.RevisionLog
	}
	return attrs
}

type writePayload struct {
	Data struct {
		Type       string         `json:"type"`
		ID         string         `json:"id,omitempty"`
		Attributes map[string]any `json:"attributes"`
	} `json:"data"`
}

// CreateNode creates a node. Callers create content unpublished unless
// NodeInput.Status says otherwise.
func (c *Client) CreateNode(ctx context.Context, bundle string, in NodeInput) (Node, error) {
	var payload writePayload
	payload.Data.Type = "node--" + bundle
	payload.Data.Attributes = in.attributes()
	doc, err := c.do(ctx, http.MethodPost, "/jsonapi/node/"+bundle, nil, payload)
	if err != nil {
		return Node{}, err
	}
	var r resource
	if err := json.Unmarshal(doc.Data, &r); err != nil {
		return Node{}, fmt.Errorf("parsing created node: %w", err)
	}
	return nodeFromResource(r), nil
}

// UpdateNode patches an existing node with the provided fields only.
func (c *Client) UpdateNode(ctx context.Context, bundle, uuid string, in NodeInput) (Node, error) {
	var payload writePayload
	payload.Data.Type = "node--" + bundle
	payload.Data.ID = uuid
	payload.Data.Attributes = in.attributes()
	doc, err := c.do(ctx, http.MethodPatch, "/jsonapi/node/"+bundle+"/"+uuid, nil, payload)
	if err != nil {
		return Node{}, err
	}
	var r resource
	if err := json.Unmarshal(doc.Data, &r); err != nil {
		return Node{}, fmt.Errorf("parsing updated node: %w", err)
	}
	return nodeFromResource(r), nil
}

// SetStatus publishes or unpublishes a node.
func (c *Client) SetStatus(ctx context.Context, bundle, uuid string, published bool, revisionLog string) (Node, error) {
	return c.UpdateNode(ctx, bundle, uuid, NodeInput{Status: &published, RevisionLog: revisionLog})
}

// ListFilterFormats returns the text formats the account may use.
func (c *Client) ListFilterFormats(ctx context.Context) ([]FilterFormat, error) {
	doc, err := c.do(ctx, http.MethodGet, "/jsonapi/filter_format/filter_format", nil, nil)
	if err != nil {
		return nil, err
	}
	var resources []resource
	if err := json.Unmarshal(doc.Data, &resources); err != nil {
		return nil, fmt.Errorf("parsing filter formats: %w", err)
	}
	formats := make([]FilterFormat, 0, len(resources))
	for _, r := range resources {
		formats = append(formats, FilterFormat{
			Machine: r.str("drupal_internal__format"),
			Name:    r.str("name"),
		})
	}
	sort.Slice(formats, func(i, j int) bool { return formats[i].Name < formats[j].Name })
	return formats, nil
}
