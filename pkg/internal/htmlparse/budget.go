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

// mergeWorkWeight is the work charged for each attribute that copyAttributes
// handles. copyAttributes puts all the attributes of the <html> or <body>
// element and of the token into a new map: about 40 ns per attribute, against
// well under 1 ns for each byte of appended text (the other unit of work).
// With this weight, a work limit of 16 Mi units stops a page of repeated
// <body> tags, each with a new attribute, after about 1,000 tags and 20 ms on
// a desktop CPU; unweighted, the same limit allowed about 5,800 tags and
// 0.7 s.
const mergeWorkWeight = 32

// budget holds the limits of one parse and the running totals. The tree
// builder charges it at the points marked "htmlparse: budget" in parse.go, and
// a charge over a limit panics with an error wrapping ErrLimitExceeded, which
// parser.parse recovers and returns.
//
// The tree estimate has two parts. The strings of every token (its data,
// attribute keys and values) are charged once, when the token is read: the
// tree holds these strings, parts of them, or concatenations of text tokens,
// never more. Every node is charged once (nodeCost plus attrCost for each
// slot of its attribute array) where the tree builder inserts it: in addChild,
// in addText for text that is foster-parented, where a comment or doctype is
// appended to the document or the <html> element, and in cloneNode for the
// clones the adoption agency algorithm inserts. Elements that the tree
// builder creates without a token of their own (an implied <tbody> or <tr>
// in a table, for example) are charged like any other.
type budget struct {
	lim  Limits
	tree int64
	work int64
}

// chargeToken charges the strings of a token: its data and the keys and
// values of its attributes.
func (b *budget) chargeToken(t *html.Token) {
	if b.lim.MaxTreeBytes <= 0 {
		return
	}
	cost := stringCost(t.Data)
	for _, at := range t.Attr {
		cost += stringCost(at.Key) + stringCost(at.Val)
	}
	b.chargeTree(cost)
}

// stringCost is the estimated heap cost of s. The allocator rounds a small
// string up to a multiple of 16 bytes (and one string under 16 bytes can keep
// a whole 16-byte block alive); larger size classes waste at most about an
// eighth.
func stringCost(s string) int64 {
	n := int64(len(s))
	if n > 256 {
		n += n / 8
	}
	return (n + 15) &^ 15
}

// chargeNode charges a node that is inserted into the tree and its attribute
// array. Its strings were charged with its token.
func (b *budget) chargeNode(n *html.Node) {
	b.chargeTree(int64(nodeCost + cap(n.Attr)*attrCost))
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
// attributes compared, or attributes merged (weighted by mergeWorkWeight).
func (b *budget) chargeWork(n int) {
	if b.lim.MaxWork <= 0 {
		return
	}
	b.work += int64(n)
	if b.work > b.lim.MaxWork {
		panic(fmt.Errorf("%w: the parse needs more than %d units of work", ErrLimitExceeded, b.lim.MaxWork))
	}
}

// cloneNode is cloneNode with the clone charged to the budget, for the clones
// that the adoption agency algorithm inserts without addChild. These clones,
// and the ones that reconstructing the active formatting elements inserts
// with addChild, are what lets a tree grow far beyond the size of its input.
func (p *parser) cloneNode(n *html.Node) *html.Node {
	clone := cloneNode(n)
	p.budget.chargeNode(clone)
	return clone
}

// copyAttributes is copyAttributes with its work and the attributes it adds
// to dst charged to the budget. A repeated <html> or <body> tag merges its
// attributes into the first one, so a page of such tags, each with a new
// attribute, does work quadratic in the number of tags.
func (p *parser) copyAttributes(dst *html.Node, src html.Token) {
	if len(src.Attr) == 0 {
		return
	}
	p.budget.chargeWork(mergeWorkWeight * (len(dst.Attr) + len(src.Attr)))
	before := cap(dst.Attr)
	copyAttributes(dst, src)
	p.budget.chargeTree(int64((cap(dst.Attr) - before) * attrCost))
}
