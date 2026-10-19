package htmlparse

import (
	"fmt"

	"golang.org/x/net/html"
)

// Estimated heap cost of the tree, in bytes. An html.Node is 104 bytes and
// lands in the 112-byte size class; an html.Attribute is 48 bytes.
const (
	nodeCost = 112
	attrCost = 48
)

// budget holds the limits of one parse and the running totals. The tree
// builder charges it at the points marked "htmlparse: budget" in parse.go, and
// a charge over a limit panics with an error wrapping ErrLimitExceeded, which
// parser.parse recovers and returns.
type budget struct {
	lim  Limits
	tree int64
	work int64
}

// chargeToken charges the node a token becomes (an element, text, comment or
// doctype node) with its attributes and text. Every token is charged, end tags
// included, which also covers the elements the tree builder creates
// implicitly (at most a few per token).
func (b *budget) chargeToken(t *html.Token) {
	if b.lim.MaxTreeBytes <= 0 {
		return
	}
	cost := int64(nodeCost + len(t.Data))
	for _, at := range t.Attr {
		cost += int64(attrCost + len(at.Key) + len(at.Val))
	}
	b.chargeTree(cost)
}

// chargeTree adds cost bytes to the estimated tree size.
func (b *budget) chargeTree(cost int64) {
	if b.lim.MaxTreeBytes <= 0 {
		return
	}
	b.tree += cost
	if b.tree > b.lim.MaxTreeBytes {
		panic(fmt.Errorf("%w: the parse tree is larger than %d bytes", ErrLimitExceeded, b.lim.MaxTreeBytes))
	}
}

// chargeWork adds n units of work: bytes copied to append text to a text node,
// or attributes compared or merged.
func (b *budget) chargeWork(n int) {
	if b.lim.MaxWork <= 0 {
		return
	}
	b.work += int64(n)
	if b.work > b.lim.MaxWork {
		panic(fmt.Errorf("%w: the parse needs more than %d units of work", ErrLimitExceeded, b.lim.MaxWork))
	}
}

// cloneNode is cloneNode with the clone charged to the budget. Reconstructing
// the active formatting elements and the adoption agency algorithm clone
// elements with their attributes; these clones are what lets a tree grow far
// beyond the size of its input.
func (p *parser) cloneNode(n *html.Node) *html.Node {
	p.budget.chargeTree(int64(nodeCost + len(n.Attr)*attrCost))
	return cloneNode(n)
}
