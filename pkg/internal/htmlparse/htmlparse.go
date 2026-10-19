// Package htmlparse is the HTML5 tree builder of golang.org/x/net/html
// (v0.61.0) with a budget on the work and memory of one parse.
//
// golang.org/x/net/html can build a tree many times larger than its input:
// the "reconstruct the active formatting elements" step clones every open
// formatting element (with its attributes) for each new paragraph, so a 1 MiB
// page can produce more than ten million nodes. Text appended to an existing
// text node is copied each time, which is quadratic in the number of appends.
// Its API gives a caller no way to stop such a parse. ParseDocument stops it
// with ErrLimitExceeded once it goes over its Limits.
//
// The files parse.go, foreign.go, doctype.go and const.go are the upstream
// files with only these changes: the package name, a dot import of
// golang.org/x/net/html (so the tree uses that package's Node type and the
// upstream code compiles unchanged), the exported Parse* functions removed,
// clone() called as cloneNode(), and the budget hooks marked
// "htmlparse: budget". node.go holds the unexported node helpers of upstream
// node.go. The tokenizer, the Node type and the renderer are used from
// golang.org/x/net/html directly. TestParseMatchesUpstream checks that this
// tree builder and golang.org/x/net/html.Parse build the same tree for the
// html5lib tree-construction tests of the golang.org/x/net version in go.mod.
//
// To update: copy the four files from the new golang.org/x/net/html version,
// re-apply the changes above, and run the tests.
package htmlparse

import (
	"errors"
	"fmt"
	"io"

	"golang.org/x/net/html"
)

// Limits bounds the work and memory of one ParseDocument call. A zero field
// disables that bound.
type Limits struct {
	// MaxTokenBytes caps the raw length of one token: a tag, a text run, a
	// comment or the content of a raw-text element such as <script>. The
	// tokenizer keeps per-attribute state for a tag, so this also bounds the
	// tokenizer's memory for a tag with very many attributes.
	MaxTokenBytes int
	// MaxTreeBytes caps the estimated heap size of the tree: its nodes,
	// attributes and text, including cloned formatting elements.
	MaxTreeBytes int64
	// MaxWork caps the bytes copied to append text to an existing text node
	// plus the attributes compared or merged for formatting elements and
	// repeated <html> and <body> tags. These grow faster than the input.
	MaxWork int64
}

// ErrLimitExceeded is wrapped by the error ParseDocument returns when the
// document goes over one of its Limits.
var ErrLimitExceeded = errors.New("htmlparse: document exceeds the parse limits")

// ParseDocument returns the parse tree for the HTML document read from r, like
// golang.org/x/net/html.Parse, but stops with an error wrapping
// ErrLimitExceeded when the document goes over lim. The input is assumed to be
// UTF-8 encoded.
func ParseDocument(r io.Reader, lim Limits) (*html.Node, error) {
	z := html.NewTokenizer(r)
	if lim.MaxTokenBytes > 0 {
		z.SetMaxBuf(lim.MaxTokenBytes)
	}
	p := &parser{
		tokenizer: z,
		doc: &html.Node{
			Type: html.DocumentNode,
		},
		scripting:  true,
		framesetOK: true,
		im:         initialIM,
		budget:     budget{lim: lim},
	}
	if err := p.parse(); err != nil {
		if errors.Is(err, html.ErrBufferExceeded) {
			return nil, fmt.Errorf("%w: a token is longer than %d bytes", ErrLimitExceeded, lim.MaxTokenBytes)
		}
		return nil, err
	}
	return p.doc, nil
}
