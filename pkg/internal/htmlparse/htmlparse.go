// Package htmlparse is the HTML5 tree builder of golang.org/x/net/html
// (v0.61.0), copied so that the proxy can bound the work and memory of one
// parse.
//
// golang.org/x/net/html builds trees that can be many times larger than the
// input: the "reconstruct the active formatting elements" step clones every
// open formatting element (with its attributes) for each new paragraph, so a
// 1 MiB page can produce more than ten million nodes. Its API gives a caller
// no way to stop a parse that grows like that.
//
// The files parse.go, foreign.go, doctype.go and const.go are the upstream
// files with only these changes: the package name, a dot import of
// golang.org/x/net/html (so the tree uses that package's Node type and the
// upstream code compiles unchanged), the exported Parse* functions removed,
// and clone() called as cloneNode(). node.go holds the unexported node helpers
// of upstream node.go. The tokenizer, the Node type and the renderer are used
// from golang.org/x/net/html directly. TestParseMatchesUpstream checks that
// this tree builder and golang.org/x/net/html.Parse build the same tree for the
// html5lib tree-construction tests of the golang.org/x/net version in go.mod.
//
// To update: copy the four files from the new golang.org/x/net/html version,
// re-apply the changes above, and run the tests.
package htmlparse

import (
	"io"

	"golang.org/x/net/html"
)

// ParseDocument returns the parse tree for the HTML document read from r, like
// golang.org/x/net/html.Parse. The input is assumed to be UTF-8 encoded.
func ParseDocument(r io.Reader) (*html.Node, error) {
	p := &parser{
		tokenizer: html.NewTokenizer(r),
		doc: &html.Node{
			Type: html.DocumentNode,
		},
		scripting:  true,
		framesetOK: true,
		im:         initialIM,
	}
	if err := p.parse(); err != nil {
		return nil, err
	}
	return p.doc, nil
}
