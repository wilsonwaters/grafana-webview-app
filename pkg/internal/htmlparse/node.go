// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package htmlparse

// The unexported node helpers of golang.org/x/net/html/node.go that the tree
// builder uses. The Node type itself is golang.org/x/net/html's, so the trees
// this package builds work with the html package's renderer and with goquery.

import (
	. "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// scopeMarkerNode is the node type of a scope marker in the list of active
// formatting elements. In golang.org/x/net/html it is the unexported constant
// after RawNode. Markers never become part of the tree.
const scopeMarkerNode = RawNode + 1

// Section 12.2.4.3 says "The markers are inserted when entering applet,
// object, marquee, template, td, th, and caption elements, and are used
// to prevent formatting from "leaking" into applet, object, marquee,
// template, td, th, and caption elements".
var scopeMarker = Node{Type: scopeMarkerNode}

// reparentChildren reparents all of src's child nodes to dst.
func reparentChildren(dst, src *Node) {
	for {
		child := src.FirstChild
		if child == nil {
			break
		}
		src.RemoveChild(child)
		dst.AppendChild(child)
	}
}

// cloneNode returns a new node with the same type, data and attributes.
// The clone has no parent, no siblings and no children.
func cloneNode(n *Node) *Node {
	m := &Node{
		Type:     n.Type,
		DataAtom: n.DataAtom,
		Data:     n.Data,
		Attr:     make([]Attribute, len(n.Attr)),
	}
	copy(m.Attr, n.Attr)
	return m
}

// nodeStack is a stack of nodes.
type nodeStack []*Node

// pop pops the stack. It will panic if s is empty.
func (s *nodeStack) pop() *Node {
	i := len(*s)
	n := (*s)[i-1]
	*s = (*s)[:i-1]
	return n
}

// top returns the most recently pushed node, or nil if s is empty.
func (s *nodeStack) top() *Node {
	if i := len(*s); i > 0 {
		return (*s)[i-1]
	}
	return nil
}

// index returns the index of the top-most occurrence of n in the stack, or -1
// if n is not present.
func (s *nodeStack) index(n *Node) int {
	for i := len(*s) - 1; i >= 0; i-- {
		if (*s)[i] == n {
			return i
		}
	}
	return -1
}

// contains returns whether a is within s.
func (s *nodeStack) contains(a atom.Atom) bool {
	for _, n := range *s {
		if n.DataAtom == a && n.Namespace == "" {
			return true
		}
	}
	return false
}

// insert inserts a node at the given index.
func (s *nodeStack) insert(i int, n *Node) {
	(*s) = append(*s, nil)
	copy((*s)[i+1:], (*s)[i:])
	(*s)[i] = n
}

// remove removes a node from the stack. It is a no-op if n is not present.
func (s *nodeStack) remove(n *Node) {
	i := s.index(n)
	if i == -1 {
		return
	}
	copy((*s)[i:], (*s)[i+1:])
	j := len(*s) - 1
	(*s)[j] = nil
	*s = (*s)[:j]
}

type insertionModeStack []insertionMode

func (s *insertionModeStack) pop() (im insertionMode) {
	i := len(*s)
	im = (*s)[i-1]
	*s = (*s)[:i-1]
	return im
}

func (s *insertionModeStack) top() insertionMode {
	if i := len(*s); i > 0 {
		return (*s)[i-1]
	}
	return nil
}
