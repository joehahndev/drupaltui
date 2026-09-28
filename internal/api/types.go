package api

import (
	"encoding/json"
	"strings"
	"time"
)

// ---- JSON:API wire types (only what we consume) ----

type document struct {
	Data   json.RawMessage `json:"data"`
	Links  linksObject     `json:"links"`
	Errors []errorObject   `json:"errors,omitempty"`
}

type linksObject map[string]struct {
	Href string `json:"href"`
}

func (l linksObject) href(name string) string {
	if v, ok := l[name]; ok {
		return v.Href
	}
	return ""
}

// resource keeps attributes as raw JSON so an absent key (field does not
// exist on the bundle) is distinguishable from a present-but-null value.
type resource struct {
	Type       string                     `json:"type"`
	ID         string                     `json:"id,omitempty"`
	Attributes map[string]json.RawMessage `json:"attributes"`
}

func (r resource) str(key string) string {
	var s string
	if raw, ok := r.Attributes[key]; ok {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

func (r resource) boolean(key string) bool {
	var b bool
	if raw, ok := r.Attributes[key]; ok {
		_ = json.Unmarshal(raw, &b)
	}
	return b
}

func (r resource) timestamp(key string) time.Time {
	raw, ok := r.Attributes[key]
	if !ok {
		return time.Time{}
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// bodyField mirrors Drupal's text_with_summary / text_long field structure.
type bodyField struct {
	Value  string `json:"value"`
	Format string `json:"format,omitempty"`
}

// ---- Domain types ----

type ContentType struct {
	Machine     string // node type machine name, e.g. "article"
	Name        string // human label
	NewRevision bool   // "Create new revision" default on the node type
	Verified    bool   // false when the node_type config could not be read
}

// Editable reports whether the TUI may write to this content type: the
// revision setting must be both verified and enabled.
func (ct ContentType) Editable() bool { return ct.Verified && ct.NewRevision }

// ReadOnlyReason explains why a bundle is not editable ("" when editable).
func (ct ContentType) ReadOnlyReason() string {
	switch {
	case ct.Editable():
		return ""
	case !ct.Verified:
		return "unverified — read only (couldn't read node type config to confirm revisions are enabled)"
	default:
		return "revisions disabled — read only (enable 'Create new revision' on this content type)"
	}
}

type Node struct {
	UUID       string
	Bundle     string
	Title      string
	Status     bool
	Changed    time.Time
	HasBody    bool   // the bundle exposes a body field on this node
	Body       string // raw body value (markdown)
	BodyFormat string
}

type FilterFormat struct {
	Machine string // filter_format machine name, e.g. "markdown_easy"
	Name    string // human label
}

type NodePage struct {
	Nodes []Node
	Next  string // absolute URL of the next page, "" when on the last page
}

// NodeInput describes a create or update. Nil pointer fields are omitted
// from the request so Drupal leaves them untouched.
type NodeInput struct {
	Title       string
	Body        *string
	BodyFormat  string
	Status      *bool
	RevisionLog string
}

func nodeFromResource(r resource) Node {
	n := Node{
		UUID:    r.ID,
		Bundle:  strings.TrimPrefix(r.Type, "node--"),
		Title:   r.str("title"),
		Status:  r.boolean("status"),
		Changed: r.timestamp("changed"),
	}
	if raw, ok := r.Attributes["body"]; ok {
		n.HasBody = true
		var b bodyField
		if err := json.Unmarshal(raw, &b); err == nil {
			n.Body = b.Value
			n.BodyFormat = b.Format
		}
	}
	return n
}
