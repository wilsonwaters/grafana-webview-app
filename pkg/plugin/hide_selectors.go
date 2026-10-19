package plugin

// Author hide selectors (CR5): the checks on the `hide` parameters of /proxy
// and the matching of the selectors against the proxied document.
//
// Any signed-in user can send `hide` values, and the selectors run against a
// document of up to the parse budget (hundreds of thousands of nodes), so the
// matching work must stay bounded:
//
//   - A request may carry at most maxHideSelectors selectors of at most
//     maxHideSelectorLen bytes each.
//   - Only a cheap subset of CSS is accepted (inspectHideSel decides).
//     cascadia's non-standard and expensive pseudo-classes
//     (:has(), :haschild(), :contains(), :containsOwn(), :matches(),
//     :matchesOwn()) and the #= regular-expression attribute operator are
//     refused, and :not() may contain only compound selectors. A /proxy
//     request with a refused selector fails with 400 before the upstream is
//     contacted.
//   - Combinators are not matched by cascadia, whose descendant and sibling
//     combinators search every ancestor or sibling again for each step, so a
//     chain of them is exponential (`x div div div div div div` takes about
//     a minute on a 100-deep tree). hideMatcher matches each compound selector
//     with cascadia and resolves the combinators in one pass over the tree,
//     keeping one bit per compound selector for the ancestors, the parent and
//     the earlier siblings.
//   - One compound selector test can be long: a :not() list can hold over a
//     hundred simple selectors, and the structural pseudo-classes
//     (:nth-child() and the like) count siblings, which can be quadratic on a
//     long list. The matcher charges each compound selector test an upper
//     bound of its cost (one unit per simple selector, plus the attributes it
//     reads and, for a structural pseudo-class, the siblings cascadia's loop
//     visits) and stops at hideMatchBudget, so the work on one document is
//     bounded whatever the page.
//   - An element that several selectors match is hidden once, so the style
//     attributes do not grow with the number of selectors.

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
)

// maxHideSelectors is the most hide selectors one /proxy request may carry. A
// request with more fails with 400; the rewriter uses only the first ones.
const maxHideSelectors = 20

// maxHideSelectorLen is the longest hide selector, in bytes after trimming. A
// longer one fails the request with 400 before it is parsed.
const maxHideSelectorLen = 256

// maxHideCompounds is the most compound selectors (the parts between
// combinators and commas) in one hide selector. hideMatcher keeps one bit per
// compound selector in a uint64.
const maxHideCompounds = 32

// hideMatchBudget is the matching work allowed on one document, for all its
// hide selectors together, in units of about one node, simple selector test,
// sibling or attribute byte visited. An ordinary page uses a few million. The
// whole budget takes 0.05 to 0.8 s on a desktop CPU, depending on the units
// the selectors use (expect up to 10 times that on a Raspberry Pi): the
// slowest are siblings in a list too long for the CPU cache, for example
// :nth-last-child(2n) over 150,000 items. A hide selector that needs more on a
// very large page (for example :nth-child(2n+1) over a list of more than about
// 10,000 items, or 20 :not() lists of over a hundred type selectors over
// 390,000 elements) fails the request (errHideMatchBudget).
const hideMatchBudget int64 = 1 << 26

// errHideMatchBudget: the hide selectors need more than hideMatchBudget work
// on this document. It wraps errResponseTooLarge, so the request fails with
// 413 like any other document that is too large to process.
var errHideMatchBudget = fmt.Errorf("%w: the hide selectors need too much work on this page", errResponseTooLarge)

// errHideSelectorSyntax marks a hide selector that cascadia cannot parse, or
// that hideMatcher cannot split into compound selectors. Such a selector is
// skipped, not refused: it never matches, so it costs nothing.
var errHideSelectorSyntax = errors.New("invalid CSS selector")

// hideSelectorRefusal is a hide selector the proxy refuses (400). reason
// completes the sentence "hide selector N ...".
type hideSelectorRefusal struct {
	reason string
}

func (e *hideSelectorRefusal) Error() string { return e.reason }

func refuseHideSelector(format string, args ...any) error {
	return &hideSelectorRefusal{reason: fmt.Sprintf(format, args...)}
}

// checkHideSelectors checks the hide selectors of one /proxy request. It
// returns the message for a 400 response, or "" when the request may go on. A
// selector that cascadia cannot parse is not refused here: the rewriter skips
// it, as it always has.
func checkHideSelectors(selectors []string) string {
	if len(selectors) > maxHideSelectors {
		return fmt.Sprintf("too many hide selectors: %d (the limit is %d)", len(selectors), maxHideSelectors)
	}
	for i, sel := range selectors {
		_, err := compileHideSelector(sel)
		var refused *hideSelectorRefusal
		if errors.As(err, &refused) {
			return fmt.Sprintf("hide selector %d %s", i+1, refused.reason)
		}
	}
	return ""
}

// hidePart is one compound selector of a hide selector.
type hidePart struct {
	// op is the combinator before the compound selector: 0 for the first
	// compound selector of a complex selector, otherwise ' ', '>', '+' or '~'.
	op   byte
	sel  cascadia.Sel
	cost hideCost
	// last is set on the rightmost compound selector of a complex selector:
	// an element it matches is hidden.
	last bool
}

// hideCost describes what one test of a compound selector against an element
// reads, so the matcher can charge an upper bound of its cost.
type hideCost struct {
	// simple is the number of nodes in the parsed compound selector: its
	// simple selectors (type selectors and * included), the :not() arguments
	// and the lists that hold them. A test visits each at most once.
	simple int
	// attrKeys has one entry per attribute test (attribute, class and id
	// selectors, :link, :checked). Each test reads every attribute key and
	// the values of the attributes with that key.
	attrKeys []string
	// structural has one entry per structural pseudo-class (:nth-child()
	// and the like), which loops over the element's siblings.
	structural []structuralTest
	// children is set when a test reads the element's children (:empty).
	children bool
}

// structuralTest is one structural pseudo-class: cascadia's
// nthPseudoClassSelector (a, b, last, ofType) or onlyChildPseudoClassSelector
// (only, ofType).
type structuralTest struct {
	a, b         int64
	last, ofType bool
	only         bool
}

// compileHideSelector parses one hide selector into its compound selectors.
// The error is a *hideSelectorRefusal for a selector the proxy refuses, or
// wraps errHideSelectorSyntax for one that is skipped.
func compileHideSelector(raw string) ([]hidePart, error) {
	sel := strings.TrimSpace(raw)
	if len(sel) > maxHideSelectorLen {
		return nil, refuseHideSelector("is longer than %d bytes", maxHideSelectorLen)
	}
	if sel == "" {
		return nil, errHideSelectorSyntax
	}
	group, err := cascadia.ParseGroup(sel)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errHideSelectorSyntax, err)
	}
	// The refusals are decided on what cascadia parsed, not on the text.
	if err := inspectHideSel(reflect.ValueOf(group), inGroup, &hideCost{}); err != nil {
		return nil, err
	}

	complexes := splitSelectorGroup(sel)
	if complexes == nil {
		return nil, errHideSelectorSyntax
	}
	count := 0
	for _, cx := range complexes {
		count += len(cx)
	}
	if count > maxHideCompounds {
		return nil, refuseHideSelector("has more than %d compound selectors", maxHideCompounds)
	}

	// Parse each compound selector on its own and check that the parts give
	// back exactly what cascadia parsed for the whole selector, so the
	// matcher and cascadia agree on the meaning.
	parts := make([]hidePart, 0, count)
	rebuilt := make([]string, 0, len(complexes))
	for _, cx := range complexes {
		var b strings.Builder
		for k, raw := range cx {
			text := raw.text
			if text == "*|*" {
				// cascadia reads *|* as * only when more text follows it, so
				// on its own it is a syntax error. ParseGroup has read this
				// part of the whole selector as *.
				text = "*"
			}
			compound, err := cascadia.Parse(text)
			if err != nil {
				return nil, fmt.Errorf("%w: %w", errHideSelectorSyntax, err)
			}
			var cost hideCost
			if err := inspectHideSel(reflect.ValueOf(compound), inCompound, &cost); err != nil {
				return nil, errHideSelectorSyntax
			}
			if k > 0 {
				b.WriteString(" " + string(raw.op) + " ")
			}
			b.WriteString(compound.String())
			parts = append(parts, hidePart{op: raw.op, sel: compound, cost: cost, last: k == len(cx)-1})
		}
		rebuilt = append(rebuilt, b.String())
	}
	if strings.Join(rebuilt, ", ") != group.String() {
		return nil, errHideSelectorSyntax
	}
	return parts, nil
}

// hideSelContext is where inspectHideSel is in the selector.
type hideSelContext int

const (
	inGroup    hideSelContext = iota // the whole selector: combinators allowed
	inCompound                       // one compound selector: no combinators
	inNot                            // the argument of :not(): no combinators
)

// cascadiaPkg is the package path of cascadia's selector types.
const cascadiaPkg = "github.com/andybalholm/cascadia"

// inspectHideSel walks a parsed cascadia selector and refuses the parts the
// proxy does not support. It adds the cost of each test to cost: one simple
// unit for every node it visits, and the attribute, sibling and child reads.
//
// cascadia does not export its selector types, so the walk reads the type
// names and fields with reflection (read only: no unexported value is used as
// an interface). A type it does not know is refused, so a cascadia update that
// adds or renames a selector type fails closed, and the tests that expect
// standard selectors to work fail loudly.
func inspectHideSel(v reflect.Value, ctx hideSelContext, cost *hideCost) error {
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return errHideSelectorSyntax
		}
		v = v.Elem()
	}
	if v.Type().PkgPath() != cascadiaPkg {
		return errHideSelectorSyntax
	}
	cost.simple++
	switch v.Type().Name() {
	case "SelectorGroup":
		for i := 0; i < v.Len(); i++ {
			if err := inspectHideSel(v.Index(i), ctx, cost); err != nil {
				return err
			}
		}
		return nil
	case "combinedSelector":
		switch ctx {
		case inNot:
			return refuseHideSelector("uses a combinator inside :not(), which may contain only simple selectors")
		case inCompound:
			return errHideSelectorSyntax
		}
		if err := inspectHideSel(v.FieldByName("first"), ctx, cost); err != nil {
			return err
		}
		if second := v.FieldByName("second"); !second.IsNil() {
			return inspectHideSel(second, ctx, cost)
		}
		return nil
	case "compoundSelector":
		if v.FieldByName("pseudoElement").String() != "" {
			return refuseHideSelector("uses a pseudo-element, which the proxy does not support")
		}
		sels := v.FieldByName("selectors")
		for i := 0; i < sels.Len(); i++ {
			if err := inspectHideSel(sels.Index(i), ctx, cost); err != nil {
				return err
			}
		}
		return nil
	case "tagSelector", "rootPseudoClassSelector":
		return nil
	case "classSelector":
		cost.attrKeys = append(cost.attrKeys, "class")
		return nil
	case "idSelector":
		cost.attrKeys = append(cost.attrKeys, "id")
		return nil
	case "attrSelector":
		if v.FieldByName("operation").String() == "#=" {
			return refuseHideSelector("uses the #= (regular expression) attribute operator, which the proxy does not support")
		}
		cost.attrKeys = append(cost.attrKeys, v.FieldByName("key").String())
		return nil
	case "linkPseudoClassSelector":
		cost.attrKeys = append(cost.attrKeys, "href")
		return nil
	case "checkedPseudoClassSelector":
		cost.attrKeys = append(cost.attrKeys, "checked", "type", "selected")
		return nil
	case "nthPseudoClassSelector":
		cost.structural = append(cost.structural, structuralTest{
			a:      v.FieldByName("a").Int(),
			b:      v.FieldByName("b").Int(),
			last:   v.FieldByName("last").Bool(),
			ofType: v.FieldByName("ofType").Bool(),
		})
		return nil
	case "onlyChildPseudoClassSelector":
		cost.structural = append(cost.structural, structuralTest{only: true, ofType: v.FieldByName("ofType").Bool()})
		return nil
	case "emptyElementPseudoClassSelector":
		cost.children = true
		return nil
	case "relativePseudoClassSelector":
		name := v.FieldByName("name").String()
		if name != "not" {
			return refuseHideSelector("uses :%s(), which the proxy does not support", name)
		}
		return inspectHideSel(v.FieldByName("match"), inNot, cost)
	case "containsPseudoClassSelector":
		return refuseHideSelector("uses %s, which the proxy does not support", ownVariant(v, ":contains()", ":containsOwn()"))
	case "regexpPseudoClassSelector":
		return refuseHideSelector("uses %s, which the proxy does not support", ownVariant(v, ":matches()", ":matchesOwn()"))
	case "langPseudoClassSelector":
		return refuseHideSelector("uses :lang(), which the proxy does not support")
	case "inputPseudoClassSelector":
		return refuseHideSelector("uses :input, which the proxy does not support")
	case "enabledPseudoClassSelector":
		return refuseHideSelector("uses :enabled, which the proxy does not support")
	case "disabledPseudoClassSelector":
		return refuseHideSelector("uses :disabled, which the proxy does not support")
	case "neverMatchSelector":
		return refuseHideSelector("uses %s, which the proxy does not support", v.FieldByName("value").String())
	}
	return refuseHideSelector("uses a selector feature that the proxy does not support")
}

// ownVariant names a :contains() or :matches() selector by its `own` field.
func ownVariant(v reflect.Value, plain, own string) string {
	if v.FieldByName("own").Bool() {
		return own
	}
	return plain
}

// rawHidePart is one compound selector of a selector group, as source text.
type rawHidePart struct {
	op   byte // as hidePart.op
	text string
}

// splitSelectorGroup splits a selector group at its top-level commas and
// combinators into compound selectors. It reads strings, escapes, comments,
// brackets and parentheses the way cascadia does, so a combinator inside an
// attribute value or an argument is not a split point. It returns nil when a
// part is empty. compileHideSelector checks the result against cascadia's own
// parse.
//
// A part ends after its last character that is not top-level whitespace or a
// comment. Whitespace inside an escape (`.a\ ` is the class "a ") is part of
// the selector, so the text is not trimmed.
func splitSelectorGroup(s string) [][]rawHidePart {
	var (
		complexes [][]rawHidePart
		current   []rawHidePart
		op        byte
		start     int // where the current part starts
		end       int // the index after the current part's last character
		depth     int
		empty     bool
	)
	flush := func() {
		text := ""
		if end > start {
			text = s[start:end]
		}
		if text == "" {
			empty = true
		}
		current = append(current, rawHidePart{op: op, text: text})
	}
	i := skipSelectorSpace(s, 0)
	start = i
	for i < len(s) {
		c := s[i]
		switch {
		case c == '\\':
			i = skipSelectorEscape(s, i)
		case isSelectorComment(s, i) && depth > 0:
			// A comment is whitespace, also inside brackets.
			i = skipSelectorSpace(s, i)
		case isSelectorComment(s, i) || depth == 0 && isSelectorSpace(c):
			// Top-level whitespace: before a comma, a combinator or the end it
			// is ignored, otherwise it is a descendant combinator.
			i = skipSelectorSpace(s, i)
			if i < len(s) && !strings.ContainsRune(",>+~", rune(s[i])) {
				flush()
				op = ' '
				start = i
			}
			continue
		case c == '"' || c == '\'':
			i = skipSelectorString(s, i)
		case c == '(' || c == '[':
			depth++
			i++
		case c == ')' || c == ']':
			depth--
			i++
		case depth > 0:
			i++
		case c == ',':
			flush()
			complexes = append(complexes, current)
			current = nil
			op = 0
			i = skipSelectorSpace(s, i+1)
			start = i
			continue
		case c == '>' || c == '+' || c == '~':
			flush()
			op = c
			i = skipSelectorSpace(s, i+1)
			start = i
			continue
		default:
			i++
		}
		end = i
	}
	flush()
	complexes = append(complexes, current)
	if empty || depth != 0 {
		return nil
	}
	return complexes
}

// isSelectorSpace reports whether c is whitespace to cascadia.
func isSelectorSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f'
}

// isSelectorComment reports whether a terminated /* comment */ starts at
// s[i]. cascadia skips such a comment as whitespace; an unterminated one is
// not a comment.
func isSelectorComment(s string, i int) bool {
	return strings.HasPrefix(s[i:], "/*") && strings.Contains(s[i+2:], "*/")
}

// skipSelectorSpace returns the index after the whitespace and comments at
// s[i].
func skipSelectorSpace(s string, i int) int {
	for i < len(s) {
		switch {
		case isSelectorSpace(s[i]):
			i++
		case isSelectorComment(s, i):
			i += 2 + strings.Index(s[i+2:], "*/") + 2
		default:
			return i
		}
	}
	return i
}

// skipSelectorEscape returns the index after the backslash escape at s[i]:
// up to six hex digits and one optional whitespace character (CRLF counts as
// one), or one other character. This is cascadia's parseEscape.
func skipSelectorEscape(s string, i int) int {
	j := i + 1
	if j >= len(s) {
		return len(s)
	}
	if !isHexDigit(s[j]) {
		return j + 1
	}
	for j < len(s) && j < i+7 && isHexDigit(s[j]) {
		j++
	}
	switch {
	case j < len(s) && s[j] == '\r':
		j++
		if j < len(s) && s[j] == '\n' {
			j++
		}
	case j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\f'):
		j++
	}
	return j
}

func isHexDigit(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

// skipSelectorString returns the index after the quoted string at s[i]. A
// backslash escapes the next character.
func skipSelectorString(s string, i int) int {
	quote := s[i]
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case quote:
			return j + 1
		}
	}
	return len(s)
}

// applyHideSelectors applies the author hide selectors (CR5) to the proxied
// document: every element a valid selector matches gets
// `display:none!important` appended to its inline style, once however many
// selectors match it. An empty or nil hideSelectors is a no-op, so the output
// is byte-identical to a rewrite without hide selectors.
//
// /proxy refuses a request with a selector that is refused here (see
// checkHideSelectors), so the skipping below is a second line of defence for
// callers that do not go through ServeHTTP. A selector that is too long,
// cannot be parsed or is refused is skipped without affecting the others, and
// only the first maxHideSelectors usable selectors are applied. When the
// matching work reaches hideMatchBudget it returns errHideMatchBudget.
//
// MARKUP-INJECTION SAFETY (AC 31). The hide is NOT applied by injecting a
// <style> block built from selector text: a valid attribute selector such as
// `[x="</style><script>…"]` would, if emitted verbatim into a raw-text <style>
// element, close the style element and inject live markup. The compiled
// selectors are used only to find elements, and hideElement writes the fixed,
// selector-free hideStyleDecl into the style attribute. The renderer escapes
// attribute values and the selector text never enters the output, so markup
// injection is impossible by construction.
//
// LIMITATION (documented, accepted). Only elements present in the
// server-rendered HTML are hidden. Elements the page's scripts add later are
// not, because the proxy does not run scripts.
func applyHideSelectors(root *html.Node, hideSelectors []string) error {
	return (&hideMatcher{budget: hideMatchBudget}).apply(root, hideSelectors)
}

// apply is applyHideSelectors with the matcher's budget. The matcher keeps the
// work charged and the tests run.
func (m *hideMatcher) apply(root *html.Node, hideSelectors []string) error {
	if len(hideSelectors) == 0 {
		return nil
	}
	applied := 0
	for _, raw := range hideSelectors {
		if applied >= maxHideSelectors {
			break
		}
		parts, err := compileHideSelector(raw)
		if err != nil {
			continue
		}
		applied++
		matched, err := m.match(root, parts)
		if err != nil {
			return err
		}
		for _, n := range matched {
			hideElement(n)
		}
	}
	return nil
}

// hideMatcher finds the elements a hide selector matches in one pass over the
// tree. Bit b of an element's mask is set when the element matches the
// compound selectors of its complex selector up to and including part b, with
// the combinators between them satisfied. A part's combinator is checked
// against the OR of the masks of the ancestors (descendant), the parent's
// mask (child), the previous sibling's mask (next sibling) or the OR of the
// previous siblings' masks (subsequent sibling), so each part is tested at
// most once per element. This gives the same result as cascadia's combinator
// matching, in linear time.
type hideMatcher struct {
	parts   []hidePart
	final   uint64 // the bits of the parts with last set
	matched []*html.Node
	work    int64 // the work charged so far
	budget  int64
	tests   int64 // the compound selector tests run so far
}

// match returns the elements below root that parts match, in document order.
// The next call reuses the returned slice, so that 20 selectors that each
// match most of a large page do not leave 20 slices of garbage.
func (m *hideMatcher) match(root *html.Node, parts []hidePart) ([]*html.Node, error) {
	m.parts = parts
	m.final = 0
	for b, p := range parts {
		if p.last {
			m.final |= uint64(1) << b
		}
	}
	m.matched = m.matched[:0]
	if err := m.visitChildren(root, 0, 0); err != nil {
		return nil, err
	}
	return m.matched, nil
}

// charge adds cost units of work and fails once the budget is spent.
func (m *hideMatcher) charge(cost int64) error {
	m.work += cost
	if m.work > m.budget {
		return errHideMatchBudget
	}
	return nil
}

// visitChildren matches the children of parent and their subtrees. ancestors
// is the OR of the masks of parent and its ancestors, parentMask the mask of
// parent.
func (m *hideMatcher) visitChildren(parent *html.Node, ancestors, parentMask uint64) error {
	siblings := &siblingStops{parent: parent}
	for c := parent.FirstChild; c != nil; c = c.NextSibling {
		siblings.size++
	}
	if err := m.charge(int64(siblings.size)); err != nil {
		return err
	}
	// prevAll is the OR of the masks of the earlier siblings; prevAdjacent
	// is the mask of the previous sibling that is not text or a comment
	// (cascadia's `+` skips only those).
	var prevAll, prevAdjacent uint64
	pos := 0
	for c := parent.FirstChild; c != nil; c = c.NextSibling {
		var mask uint64
		if c.Type == html.ElementNode {
			var err error
			mask, err = m.evaluate(c, pos, siblings, relatedMasks{
				ancestors: ancestors, parent: parentMask, prevAll: prevAll, prevAdjacent: prevAdjacent,
			})
			if err != nil {
				return err
			}
			if mask&m.final != 0 {
				m.matched = append(m.matched, c)
			}
		}
		if c.Type != html.TextNode && c.Type != html.CommentNode {
			prevAdjacent = mask
		}
		prevAll |= mask
		pos++
		if c.FirstChild != nil {
			if err := m.visitChildren(c, ancestors|mask, mask); err != nil {
				return err
			}
		}
	}
	return nil
}

// relatedMasks are the masks an element's combinators are checked against.
type relatedMasks struct {
	ancestors    uint64 // OR of the ancestors' masks (descendant combinator)
	parent       uint64 // the parent's mask (child combinator)
	prevAll      uint64 // OR of the earlier siblings' masks (`~`)
	prevAdjacent uint64 // the previous non-text, non-comment sibling's mask (`+`)
}

// evaluate returns the mask of element n, the child at position pos of
// siblings.parent. It tests a part only when its combinator is satisfied, and
// charges each test before running it.
func (m *hideMatcher) evaluate(n *html.Node, pos int, siblings *siblingStops, rel relatedMasks) (uint64, error) {
	var mask uint64
	for b := range m.parts {
		p := &m.parts[b]
		if p.op != 0 {
			var related uint64
			switch p.op {
			case ' ':
				related = rel.ancestors
			case '>':
				related = rel.parent
			case '+':
				related = rel.prevAdjacent
			default: // '~'
				related = rel.prevAll
			}
			if related&(uint64(1)<<(b-1)) == 0 {
				continue
			}
		}
		if err := m.charge(m.testCost(n, pos, siblings, &p.cost)); err != nil {
			return 0, err
		}
		m.tests++
		if p.sel.Match(n) {
			mask |= uint64(1) << b
		}
	}
	return mask, nil
}

// testCost is an upper bound of the work of testing a compound selector with
// the given cost against n, the child at position pos of siblings.parent: one
// unit, plus one for each node of the parsed compound selector (each simple
// selector, :not() argument and list), plus every attribute key and the values
// with the tested key for each attribute test, plus the siblings each
// structural pseudo-class visits, plus the children (and their text) :empty
// reads.
func (m *hideMatcher) testCost(n *html.Node, pos int, siblings *siblingStops, cost *hideCost) int64 {
	units := 1 + int64(cost.simple)
	for _, key := range cost.attrKeys {
		units += int64(len(n.Attr))
		for _, a := range n.Attr {
			if a.Key == key {
				units += int64(len(a.Val))
			}
		}
	}
	for _, t := range cost.structural {
		units += int64(m.structuralCost(t, n, pos, siblings))
	}
	if cost.children {
		// :empty trims the text of each text child.
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			units++
			if c.Type == html.TextNode {
				units += int64(len(c.Data))
			}
		}
	}
	return units
}

// structuralCost is the number of siblings cascadia's loop for t visits when
// it tests n, the child at position pos of siblings.parent. The loops
// (cascadia's nthChildMatch, simpleNthChildMatch, simpleNthLastChildMatch and
// onlyChildPseudoClassSelector.Match) walk the children from one end and stop
// at n, at the k-th element (of n's type for the -of-type variants), or at
// the other end:
//
//   - :nth-child(b) and :first-child (a == 0) stop at n or at the max(b, 1)-th
//     element from the start; :nth-last-child(b) and :last-child likewise
//     from the end;
//   - :nth-child(an+b) with a != 0 stops at n; :nth-last-child(an+b) visits
//     every sibling;
//   - :only-child stops at the second element.
func (m *hideMatcher) structuralCost(t structuralTest, n *html.Node, pos int, siblings *siblingStops) int {
	tag := ""
	if t.ofType {
		tag = n.Data
	}
	fromEnd := siblings.size - 1 - pos
	switch {
	case t.only:
		if stop := m.stop(siblings, 2, tag, false); stop >= 0 {
			return stop + 1
		}
		return siblings.size
	case t.a == 0 && !t.last:
		if stop := m.stop(siblings, max(t.b, 1), tag, false); stop >= 0 && stop < pos {
			return stop + 1
		}
		return pos + 1
	case t.a == 0:
		if stop := m.stop(siblings, max(t.b, 1), tag, true); stop >= 0 && stop < fromEnd {
			return stop + 1
		}
		return fromEnd + 1
	case !t.last:
		return pos + 1
	default:
		return siblings.size
	}
}

// siblingStops caches, for the children of one parent, where the loops of
// the structural pseudo-classes stop.
type siblingStops struct {
	parent *html.Node
	size   int // the number of children, of any node type
	stops  map[stopKey]int
}

// stopKey names the k-th element (of type tag, or any element when tag is
// empty), counted from the start, or from the end when fromEnd is set.
type stopKey struct {
	k       int64
	tag     string
	fromEnd bool
}

// stop returns the distance, in children, from the start (or the end) of
// siblings.parent to its k-th element of type tag, or -1 when there are fewer
// than k. The scan is charged to the budget (checked by the caller's next
// charge) and its result cached.
func (m *hideMatcher) stop(siblings *siblingStops, k int64, tag string, fromEnd bool) int {
	key := stopKey{k: k, tag: tag, fromEnd: fromEnd}
	if stop, ok := siblings.stops[key]; ok {
		return stop
	}
	if siblings.stops == nil {
		siblings.stops = make(map[stopKey]int)
	}
	stop, steps := -1, 0
	var count int64
	c := siblings.parent.FirstChild
	if fromEnd {
		c = siblings.parent.LastChild
	}
	for c != nil {
		if c.Type == html.ElementNode && (tag == "" || c.Data == tag) {
			count++
			if count == k {
				stop = steps
				break
			}
		}
		steps++
		if fromEnd {
			c = c.PrevSibling
		} else {
			c = c.NextSibling
		}
	}
	m.work += int64(steps + 1)
	siblings.stops[key] = stop
	return stop
}

// hideElement appends hideStyleDecl to n's inline style attribute, preserving
// any existing inline style. The existing style is terminated with a ';'
// before the appended declaration (added only when it does not already end in
// one) so the two declarations stay distinct. The renderer escapes the value
// on output; no selector text or HTML is ever concatenated into the markup.
//
// An element whose style already ends with the declaration (it was hidden by
// an earlier selector) is left as it is. Appending again would add nothing,
// and with 20 selectors that all match a large page the copies of the growing
// style values would take hundreds of MiB.
func hideElement(n *html.Node) {
	for i := range n.Attr {
		if n.Attr[i].Key != "style" {
			continue
		}
		existing := strings.TrimSpace(n.Attr[i].Val)
		switch {
		case existing == "":
			n.Attr[i].Val = hideStyleDecl
		case existing == hideStyleDecl || strings.HasSuffix(existing, ";"+hideStyleDecl):
			// Already hidden.
		case strings.HasSuffix(existing, ";"):
			n.Attr[i].Val = existing + hideStyleDecl
		default:
			n.Attr[i].Val = existing + ";" + hideStyleDecl
		}
		return
	}
	n.Attr = append(n.Attr, html.Attribute{Key: "style", Val: hideStyleDecl})
}
