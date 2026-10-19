package plugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andybalholm/cascadia"
	"github.com/wilsonwaters/webview/pkg/internal/htmlparse"
	"golang.org/x/net/html"
)

// hideSelectorCase is one entry of testdata/hide_selectors.json, the table the
// backend and the panel editor (src/panels/webview/hideSelectors.test.ts) are
// both tested against.
type hideSelectorCase struct {
	Selector string `json:"selector"`
	Allowed  bool   `json:"allowed"`
	Reason   string `json:"reason"`
}

func loadHideSelectorCases(t *testing.T) []hideSelectorCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/hide_selectors.json")
	if err != nil {
		t.Fatalf("read testdata/hide_selectors.json: %v", err)
	}
	var table struct {
		Cases []hideSelectorCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatalf("parse testdata/hide_selectors.json: %v", err)
	}
	if len(table.Cases) == 0 {
		t.Fatal("testdata/hide_selectors.json has no cases")
	}
	return table.Cases
}

// TestHideSelectorPolicyTable covers CR5 hardening: the shared table of
// accepted and refused hide selectors. Accepted selectors compile; refused ones
// fail the request with a message naming the feature.
func TestHideSelectorPolicyTable(t *testing.T) {
	for _, tc := range loadHideSelectorCases(t) {
		t.Run(tc.Selector, func(t *testing.T) {
			// The cases must be valid CSS for cascadia: the table is about
			// what the proxy refuses, not about syntax.
			if _, err := cascadia.ParseGroup(tc.Selector); err != nil {
				t.Fatalf("test precondition: cascadia cannot parse %q: %v", tc.Selector, err)
			}
			msg := checkHideSelectors([]string{tc.Selector})
			parts, err := compileHideSelector(tc.Selector)
			if tc.Allowed {
				if msg != "" || err != nil {
					t.Fatalf("checkHideSelectors = %q, compile error = %v; want the selector accepted", msg, err)
				}
				if len(parts) == 0 {
					t.Fatal("accepted selector compiled to no parts")
				}
				return
			}
			var refused *hideSelectorRefusal
			if !errors.As(err, &refused) {
				t.Fatalf("compile error = %v, want a refusal", err)
			}
			if !strings.HasPrefix(msg, "hide selector 1 ") || !strings.Contains(msg, tc.Reason) {
				t.Fatalf("checkHideSelectors = %q, want it to name selector 1 and contain %q", msg, tc.Reason)
			}
		})
	}
}

// TestHideSelectorLimits covers the count, length and compound-selector caps.
func TestHideSelectorLimits(t *testing.T) {
	repeat := func(sel string, n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = sel
		}
		return out
	}
	longest := "." + strings.Repeat("a", maxHideSelectorLen-1)
	compounds := func(n int) string { return strings.TrimSpace(strings.Repeat("a ", n)) }

	cases := []struct {
		name      string
		selectors []string
		want      string // "" = accepted, else a substring of the message
	}{
		{"no selectors", nil, ""},
		{"at the count limit", repeat(".a", maxHideSelectors), ""},
		{"over the count limit", repeat(".a", maxHideSelectors+1), "too many hide selectors: 21 (the limit is 20)"},
		{"at the length limit", []string{longest}, ""},
		{"over the length limit", []string{".a", longest + "a"}, "hide selector 2 is longer than 256 bytes"},
		{"surrounding whitespace is not counted", []string{"  " + longest + "  "}, ""},
		{"at the compound limit", []string{compounds(maxHideCompounds)}, ""},
		{"over the compound limit", []string{compounds(maxHideCompounds + 1)}, "has more than 32 compound selectors"},
		{"compound limit counts every complex selector", []string{strings.Repeat("a, ", maxHideCompounds) + "a"}, "has more than 32 compound selectors"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := checkHideSelectors(tc.selectors)
			if tc.want == "" && got != "" {
				t.Fatalf("checkHideSelectors = %q, want accepted", got)
			}
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("checkHideSelectors = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

// TestHideSelectorSyntaxErrorsAreSkipped: a selector cascadia cannot parse is
// not refused (the request goes on) and the rewriter skips it, as before.
func TestHideSelectorSyntaxErrorsAreSkipped(t *testing.T) {
	for _, sel := range []string{">>>bad", "div[", "a:::", "{}", ".a >", "p::before", ":has(p", "a:unknown"} {
		t.Run(sel, func(t *testing.T) {
			if msg := checkHideSelectors([]string{sel}); msg != "" {
				t.Fatalf("checkHideSelectors = %q, want no refusal for a syntax error", msg)
			}
			if _, err := compileHideSelector(sel); !errors.Is(err, errHideSelectorSyntax) {
				t.Fatalf("compile error = %v, want errHideSelectorSyntax", err)
			}
		})
	}
}

// hideMatchFixture has every kind of sibling, text, comment and nesting the
// combinators and structural pseudo-classes look at.
const hideMatchFixture = `<!DOCTYPE html>
<html><head><title>t</title></head>
<body class="page">
  <header id="site-header"><nav><a href="/a" class="x">A</a> <a class="y">B</a></nav></header>
  <!-- comment -->
  <div class="banner ad"><span class="close">x</span><p>one</p>text<p class="keep">two</p></div>
  <ul><li>1</li><li class="b">2</li><!-- c --><li>3</li> <li><ul><li>n1</li><li>n2</li></ul></li></ul>
  <h1>h</h1><p>p1</p><div>d</div><p>p2</p><p data-x="a > b, c">p3</p>
  <table><tr><td>1</td></tr><tr><td>2</td></tr><tr><td>3</td><td>4</td></tr></table>
  <form><input type="checkbox" checked><input type="text"><select><option selected>o</option></select></form>
  <div></div><div> </div><div><!-- only a comment --></div>
  <section><div><div><div><span class="deep">s</span></div></div></div></section>
  <p id="123">id</p><p class="a:b">colon</p><p lang="en-GB">lang</p>
  <div id="b" class="a"><p class="a">a</p><div class="a"><p class="b">b</p></div></div>
</body></html>`

func parseHideFixture(t *testing.T, doc string) *html.Node {
	t.Helper()
	root, err := htmlparse.ParseDocument(strings.NewReader(doc), htmlParseLimits)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return root
}

// TestHideMatcherAgreesWithCascadia: the linear matcher hides exactly the
// elements cascadia's own matching finds, for every accepted selector in the
// shared table and a set of combinator mixes.
func TestHideMatcherAgreesWithCascadia(t *testing.T) {
	root := parseHideFixture(t, hideMatchFixture)
	selectors := []string{
		"body div", "body > div", "div > p", "p + p", "p ~ p", "h1 + p", "h1 ~ div ~ p",
		"li + li", "ul li", "ul > li", "ul ul li", "section div span", "section > div span",
		"section div > span", "div div div span", "* > p", "body *", ":not(div) > p",
		"li:nth-child(2n) + li", "tr:nth-child(odd) td", "p:not(.keep) ~ p", "nav a + a",
		".banner p:last-child", "a, li, td", "div.ad > p, ul li:first-child", "html", "head title",
		"td:only-child", "option:checked", "div:not(:empty)", ".a .a", ".a > .a", "div.a p.b",
		"#b .a ~ .a", "div ~ div ~ section span", "html body div div div div span",
		"li ~ li ~ li", "body > :first-child + *", "p:nth-last-child(1)", "td:nth-of-type(2)",
		"tr > td + td", "ul li ul li:last-child", "[data-x] , [lang]", "* * * * span",
	}
	for _, tc := range loadHideSelectorCases(t) {
		if tc.Allowed {
			selectors = append(selectors, tc.Selector)
		}
	}
	for _, sel := range selectors {
		t.Run(sel, func(t *testing.T) {
			want := cascadia.MustCompile(sel).MatchAll(root)
			parts, err := compileHideSelector(sel)
			if err != nil {
				t.Fatalf("compileHideSelector: %v", err)
			}
			got, err := (&hideMatcher{budget: hideMatchBudget}).match(root, parts)
			if err != nil {
				t.Fatalf("match: %v", err)
			}
			if len(got) != len(want) {
				t.Fatalf("matched %d elements, cascadia matches %d", len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("element %d: matched <%s>, cascadia matches <%s>", i, got[i].Data, want[i].Data)
				}
			}
		})
	}
}

// Random pages and selectors for TestHideMatcherAgreesWithCascadiaRandom.
var (
	randomPageTags    = []string{"div", "span", "p", "section", "ul", "li", "a", "em"}
	randomPageClasses = []string{"a", "b", "c", "a b", "b c", "ab"}
	randomSelTypes    = []string{"div", "span", "p", "section", "ul", "li", "a", "em", "body", "*"}
	randomSelSimple   = []string{
		".a", ".b", ".c", "#x", "#y", "[data-k]", `[data-k="1"]`, "[data-k~='2']", `[data-k|="1"]`,
		"[class^=a]", `[class*="b"]`, "[class$=c]", "[class!=a]", "[href]",
		":first-child", ":last-child", ":only-child", ":first-of-type", ":last-of-type", ":only-of-type",
		":nth-child(2n+1)", ":nth-child(odd)", ":nth-child(even)", ":nth-child(2)", ":nth-child(-n+2)",
		":nth-last-child(2)", ":nth-last-child(3n)", ":nth-of-type(2)", ":nth-of-type(2n)",
		":nth-last-of-type(1)", ":nth-last-of-type(odd)", ":empty", ":root", ":link",
	}
	randomSelCombinators = []string{" ", "  ", " > ", ">", " + ", "+", " ~ ", "~"}
)

// randomHidePage returns a random document of up to about 70 elements: nested
// and sibling elements of a few types with classes, ids and attributes from
// small sets, between text, whitespace, comments and empty elements, so the
// combinators and the structural pseudo-classes have cases to tell apart. The
// HTML parser then moves some elements (a <div> closes a <p>, an <a> in an <a>
// is split), as it does on real pages.
func randomHidePage(r *rand.Rand) string {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head><title>t</title></head><body>")
	elements := 20 + r.IntN(50)
	var children func(depth int)
	children = func(depth int) {
		for i := r.IntN(6); i > 0 && elements > 0; i-- {
			switch r.IntN(7) {
			case 0:
				b.WriteString("t")
			case 1:
				b.WriteString(" ")
			case 2:
				b.WriteString("<!--c-->")
			default:
				elements--
				tag := randomPageTags[r.IntN(len(randomPageTags))]
				b.WriteString("<" + tag)
				if r.IntN(2) == 0 {
					fmt.Fprintf(&b, ` class="%s"`, randomPageClasses[r.IntN(len(randomPageClasses))])
				}
				if r.IntN(6) == 0 {
					fmt.Fprintf(&b, ` id="%c"`, "xy"[r.IntN(2)])
				}
				if r.IntN(4) == 0 {
					fmt.Fprintf(&b, ` data-k="%s"`, []string{"1", "2", "1 2", "1-x"}[r.IntN(4)])
				}
				if tag == "a" && r.IntN(2) == 0 {
					b.WriteString(` href="/x"`)
				}
				b.WriteString(">")
				if depth < 6 {
					children(depth + 1)
				}
				b.WriteString("</" + tag + ">")
			}
		}
	}
	for elements > 0 {
		children(0)
	}
	b.WriteString("</body></html>")
	return b.String()
}

// randomCompound returns a random compound selector: an optional type
// selector, then up to three class, id, attribute, structural or :not()
// selectors. A :not() holds a list of compound selectors, nested up to twice.
func randomCompound(r *rand.Rand, notDepth int) string {
	var b strings.Builder
	if r.IntN(3) != 0 {
		b.WriteString(randomSelTypes[r.IntN(len(randomSelTypes))])
	}
	for k := r.IntN(4); k > 0; k-- {
		if notDepth < 2 && r.IntN(5) == 0 {
			args := make([]string, 1+r.IntN(3))
			for i := range args {
				args[i] = randomCompound(r, notDepth+1)
			}
			b.WriteString(":not(" + strings.Join(args, []string{",", ", "}[r.IntN(2)]) + ")")
		} else {
			b.WriteString(randomSelSimple[r.IntN(len(randomSelSimple))])
		}
	}
	if b.Len() == 0 {
		b.WriteString("*")
	}
	return b.String()
}

// randomHideSelector returns a random selector list of one to three complex
// selectors of one to four compound selectors, joined by any of the four
// combinators, within maxHideSelectorLen.
func randomHideSelector(r *rand.Rand) string {
	for {
		complexes := make([]string, 1+r.IntN(3))
		for i := range complexes {
			var b strings.Builder
			b.WriteString(randomCompound(r, 0))
			for k := r.IntN(4); k > 0; k-- {
				b.WriteString(randomSelCombinators[r.IntN(len(randomSelCombinators))])
				b.WriteString(randomCompound(r, 0))
			}
			complexes[i] = b.String()
		}
		sel := strings.Join(complexes, []string{",", ", ", " ,"}[r.IntN(3)])
		if len(sel) <= maxHideSelectorLen {
			return sel
		}
	}
}

// TestHideMatcherAgreesWithCascadiaRandom covers CR5 hardening: on random
// pages, the linear matcher hides exactly the elements cascadia's own
// matching finds for random selectors of all four combinators, selector
// lists, nested :not() and the structural pseudo-classes. The seeds are fixed,
// so every run checks the same 4,000 page and selector pairs.
func TestHideMatcherAgreesWithCascadiaRandom(t *testing.T) {
	const pages, selectorsPerPage = 160, 25
	pairs, nonEmpty := 0, 0
	for seed := uint64(1); seed <= pages; seed++ {
		r := rand.New(rand.NewPCG(seed, 0x5eed))
		doc := randomHidePage(r)
		root := parseHideFixture(t, doc)
		for k := 0; k < selectorsPerPage; k++ {
			sel := randomHideSelector(r)
			compiled, err := cascadia.Compile(sel)
			if err != nil {
				t.Fatalf("seed %d: cascadia cannot parse the generated selector %q: %v", seed, sel, err)
			}
			want := compiled.MatchAll(root)
			parts, err := compileHideSelector(sel)
			if err != nil {
				t.Fatalf("seed %d: compileHideSelector(%q): %v", seed, sel, err)
			}
			got, err := (&hideMatcher{budget: hideMatchBudget}).match(root, parts)
			if err != nil {
				t.Fatalf("seed %d: match(%q): %v", seed, sel, err)
			}
			if !sameNodes(got, want) {
				t.Fatalf("seed %d: selector %q matched %d elements, cascadia matches %d\npage: %s", seed, sel, len(got), len(want), doc)
			}
			pairs++
			if len(want) > 0 {
				nonEmpty++
			}
		}
	}
	// Guard the generator: most selectors must match something, or the
	// agreement says little.
	if nonEmpty < pairs/3 {
		t.Fatalf("only %d of %d random selectors match an element", nonEmpty, pairs)
	}
	t.Logf("%d page and selector pairs agree (%d match at least one element)", pairs, nonEmpty)
}

// sameNodes reports whether a and b hold the same nodes in the same order.
func sameNodes(a, b []*html.Node) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// slowLimit is the time a matching test may take: generous, and wider under
// the race detector.
func slowLimit() time.Duration {
	if raceEnabled {
		return 30 * time.Second
	}
	return 3 * time.Second
}

// deepDivs is a document of n nested <div> elements.
func deepDivs(n int) string {
	return "<html><body>" + strings.Repeat("<div>", n) + "<span>x</span>" + strings.Repeat("</div>", n) + "</body></html>"
}

// flatParagraphs is a document of n sibling <p> elements.
func flatParagraphs(n int) string {
	return "<html><body>" + strings.Repeat("<p>x", n) + "</body></html>"
}

// TestHideCombinatorChainsAreLinear covers CR5 hardening: chains of
// descendant and subsequent-sibling combinators are exponential in cascadia
// (each step searches every ancestor or sibling again), but the hide matcher
// resolves them in one pass. Each case finishes well within slowLimit with the
// same result cascadia gives on a small tree.
func TestHideCombinatorChainsAreLinear(t *testing.T) {
	cases := []struct {
		name, doc, selector string
		want                int
	}{
		{"descendant chain, no match", deepDivs(400), "span " + strings.Repeat("div ", 30) + "div", 0},
		{"descendant chain, match", deepDivs(400), strings.Repeat("div ", 30) + "span", 1},
		{"sibling chain, no match", flatParagraphs(50000), "span" + strings.Repeat(" ~ p", 30), 0},
		{"sibling chain, match", flatParagraphs(50000), strings.Repeat("p ~ ", 30) + "p", 50000 - 30},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := parseHideFixture(t, tc.doc)
			parts, err := compileHideSelector(tc.selector)
			if err != nil {
				t.Fatalf("compileHideSelector: %v", err)
			}
			start := time.Now()
			got, err := (&hideMatcher{budget: hideMatchBudget}).match(root, parts)
			elapsed := time.Since(start)
			if err != nil {
				t.Fatalf("match: %v", err)
			}
			if len(got) != tc.want {
				t.Fatalf("matched %d elements, want %d", len(got), tc.want)
			}
			if elapsed > slowLimit() {
				t.Fatalf("matching took %v, want under %v", elapsed, slowLimit())
			}
		})
	}
}

// TestHideMatchBudgetStopsQuadraticSelectors covers CR5 hardening: some
// structural pseudo-classes count siblings up to the element, which is
// quadratic on a long list. The matcher charges the siblings each test visits
// and stops at the budget, so a large page fails with errHideMatchBudget (413)
// in bounded time, while the same selector on an ordinary list still applies.
func TestHideMatchBudgetStopsQuadraticSelectors(t *testing.T) {
	big := []byte(flatParagraphs(150000))
	for _, sel := range []string{"p:nth-child(2n)", "p:nth-of-type(3n+1)", "p:nth-last-of-type(2n)", "p:not(:nth-last-child(3n))"} {
		t.Run(sel, func(t *testing.T) {
			start := time.Now()
			_, err := rewriteHTML(big, mustParseURL(t, examplePage), "text/html", []string{sel})
			elapsed := time.Since(start)
			if !errors.Is(err, errHideMatchBudget) || !errors.Is(err, errResponseTooLarge) {
				t.Fatalf("rewriteHTML error = %v, want errHideMatchBudget (a 413)", err)
			}
			if elapsed > slowLimit() {
				t.Fatalf("refusal took %v, want under %v", elapsed, slowLimit())
			}

			// On an ordinary list the same selector applies, to the elements
			// cascadia finds.
			small := flatParagraphs(500)
			want := len(cascadia.MustCompile(sel).MatchAll(parseHideFixture(t, small)))
			out, err := rewriteHTML([]byte(small), mustParseURL(t, examplePage), "text/html", []string{sel})
			if err != nil {
				t.Fatalf("500-item list: rewriteHTML: %v", err)
			}
			if got := bytes.Count(out, []byte(hideStyleDecl)); got != want {
				t.Fatalf("500-item list: %d elements hidden, cascadia matches %d", got, want)
			}
		})
	}
}

// TestHideStructuralSelectorsStayCheapOnLongLists: the structural
// pseudo-classes whose loop stops at the first or second matching sibling are
// charged only that, so they still apply to a very long list, with the result
// cascadia gives.
func TestHideStructuralSelectorsStayCheapOnLongLists(t *testing.T) {
	const items = 150000
	doc := flatParagraphs(items) + "<div>d</div>"
	root := parseHideFixture(t, doc)
	for _, sel := range []string{
		"p:first-child", "p:last-child", "div:last-child", "p:first-of-type", "p:last-of-type",
		"div:only-of-type", "p:only-child", "p:nth-child(3)", "p:nth-last-child(2)",
		"p:nth-of-type(5)", "p:nth-last-of-type(1)", "p:not(:first-child) + div", ":root",
	} {
		t.Run(sel, func(t *testing.T) {
			want := cascadia.MustCompile(sel).MatchAll(root)
			parts, err := compileHideSelector(sel)
			if err != nil {
				t.Fatalf("compileHideSelector: %v", err)
			}
			start := time.Now()
			got, err := (&hideMatcher{budget: hideMatchBudget}).match(root, parts)
			if err != nil {
				t.Fatalf("match: %v", err)
			}
			if elapsed := time.Since(start); elapsed > slowLimit() {
				t.Fatalf("matching took %v, want under %v", elapsed, slowLimit())
			}
			if len(got) != len(want) {
				t.Fatalf("matched %d elements, cascadia matches %d", len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("element %d differs from cascadia's match", i)
				}
			}
		})
	}
}

// TestProxyRefusesExpensiveHideSelectors is the CR5 hardening security test:
// a /proxy request whose hide selectors break the limits fails with a JSON
// 400 before the upstream is contacted, so a :has() over a page of a hundred
// thousand elements costs nothing. A request within the limits is served.
func TestProxyRefusesExpensiveHideSelectors(t *testing.T) {
	var hits atomic.Int32
	page := flatParagraphs(100000)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	defer upstream.Close()
	p := newTestHandler(t, settingsWith(allowExample(DomainOptions{})), upstream)

	many := make([]string, maxHideSelectors+1)
	for i := range many {
		many[i] = ".a"
	}
	cases := []struct {
		name      string
		selectors []string
		want      string
	}{
		{":has() over a large page", []string{"body:has(p) p"}, "hide selector 1 uses :has()"},
		{":has() nested in :not()", []string{".x", "p:not(:has(span))"}, "hide selector 2 uses :has()"},
		{":haschild()", []string{"body:haschild(p)"}, ":haschild()"},
		{":contains()", []string{`p:contains("x")`}, ":contains()"},
		{":containsOwn()", []string{"p:containsOwn(x)"}, ":containsOwn()"},
		{":matches()", []string{"p:matches(x+)"}, ":matches()"},
		{":matchesOwn()", []string{"p:matchesOwn(x+)"}, ":matchesOwn()"},
		{"regular-expression attribute", []string{"[class#=(a|b)]"}, "#="},
		{"combinator inside :not()", []string{"p:not(body p)"}, ":not()"},
		{"too many selectors", many, "too many hide selectors"},
		{"selector too long", []string{"." + strings.Repeat("a", maxHideSelectorLen)}, "longer than 256 bytes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := hits.Load()
			start := time.Now()
			rec := doProxyHides(p, "http://example.com/page", tc.selectors)
			elapsed := time.Since(start)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400 (body=%q)", rec.Code, rec.Body.String())
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Fatalf("Content-Type %q, want application/json", ct)
			}
			var body jsonDenial
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body is not JSON: %v (%q)", err, rec.Body.String())
			}
			if body.Reason != denialReasonBadRequest || !strings.Contains(body.Message, tc.want) {
				t.Fatalf("body = %+v, want reason %q and a message containing %q", body, denialReasonBadRequest, tc.want)
			}
			if hits.Load() != before {
				t.Fatal("the upstream was contacted for a refused request")
			}
			if elapsed > time.Second {
				t.Fatalf("refusal took %v, want under 1s", elapsed)
			}
		})
	}

	// Control: standard selectors over the same page are served and applied.
	rec := doProxyHides(p, "http://example.com/page", []string{"p:first-child", "body > p + p"})
	if rec.Code != http.StatusOK {
		t.Fatalf("control: status %d, want 200 (body=%q)", rec.Code, rec.Body.String())
	}
	if n := strings.Count(rec.Body.String(), hideStyleDecl); n != 100000 {
		t.Fatalf("control: %d elements hidden, want 100000", n)
	}
}

// TestProxyHideMatchBudgetIs413: a selector that runs out of matching budget
// on a very large page fails the request with 413 and a message naming the
// hide selectors.
func TestProxyHideMatchBudgetIs413(t *testing.T) {
	page := flatParagraphs(150000)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	defer upstream.Close()
	p := newTestHandler(t, settingsWith(allowExample(DomainOptions{})), upstream)

	rec := doProxyHides(p, "http://example.com/page", []string{"p:nth-child(2n)"})
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413 (body=%q)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "too large for the hide selectors") {
		t.Fatalf("body %q does not name the hide selectors", rec.Body.String())
	}
}

// TestHideSelectorRewriterSkipsRefusedSelectors: callers that do not go
// through /proxy still never run a refused selector. The rewriter skips it
// quickly and applies the valid selectors next to it.
func TestHideSelectorRewriterSkipsRefusedSelectors(t *testing.T) {
	big := []byte(flatParagraphs(100000) + `<div class="ad">x</div>`)
	start := time.Now()
	out, err := rewriteHTML(big, mustParseURL(t, examplePage), "text/html", []string{"body:has(p) p", ".ad"})
	if err != nil {
		t.Fatalf("rewriteHTML: %v", err)
	}
	if elapsed := time.Since(start); elapsed > slowLimit() {
		t.Fatalf("rewrite took %v, want under %v", elapsed, slowLimit())
	}
	if n := bytes.Count(out, []byte(hideStyleDecl)); n != 1 {
		t.Fatalf("%d elements hidden, want only the .ad element", n)
	}
}

// largestElementPage is the number of <p> elements in bareParagraphs that is
// close to the most the parse limits allow (about 393,000).
const largestElementPage = 390000

// bareParagraphs is a document of n sibling <p> elements without text.
func bareParagraphs(n int) string {
	return "<html><body>" + strings.Repeat("<p>", n) + "</body></html>"
}

// longNotSelectors returns n distinct hide selectors `*:not(a,a,…,a,p,xK)` of
// 124 type selectors each, the most that fit in maxHideSelectorLen. Each test
// of a <p> compares its type with the list up to the p, 123 times.
func longNotSelectors(n int) []string {
	out := make([]string, n)
	for k := range out {
		tail := fmt.Sprintf("p,x%d)", k)
		var b strings.Builder
		b.WriteString("*:not(")
		for b.Len()+len("a,")+len(tail) <= maxHideSelectorLen {
			b.WriteString("a,")
		}
		b.WriteString(tail)
		out[k] = b.String()
	}
	return out
}

// matchAllSelectors returns n distinct hide selectors `p:not(.xK)` that each
// match every <p> element.
func matchAllSelectors(n int) []string {
	out := make([]string, n)
	for k := range out {
		out[k] = fmt.Sprintf("p:not(.x%d)", k)
	}
	return out
}

// TestHideMatchBudgetChargesEverySimpleSelector covers CR5 hardening: a test
// of a compound selector is charged one unit for each simple selector in it,
// so the 20 longest :not() lists of type selectors on the largest page the
// parser allows run out of budget (413) after a bounded number of type
// comparisons. When only attributes and siblings were charged, these took
// 3.7 to 5 s on a desktop and were served.
func TestHideMatchBudgetChargesEverySimpleSelector(t *testing.T) {
	selectors := longNotSelectors(maxHideSelectors)
	if msg := checkHideSelectors(selectors); msg != "" {
		t.Fatalf("test precondition: checkHideSelectors = %q, want the selectors accepted", msg)
	}
	page := bareParagraphs(largestElementPage)

	// The work: count the compound selector tests the matcher runs. Each one
	// compares a <p> with every type in the list before the p, so the type
	// comparisons (counted from the selector text, not from the matcher's own
	// cost) must stay within the budget.
	typesPerTest := int64(strings.Count(selectors[0], ","))
	for _, sel := range selectors {
		if n := int64(strings.Count(sel, ",")); n < typesPerTest {
			typesPerTest = n
		}
	}
	m := &hideMatcher{budget: hideMatchBudget}
	err := m.apply(parseHideFixture(t, page), selectors)
	if !errors.Is(err, errHideMatchBudget) {
		t.Fatalf("apply error = %v, want errHideMatchBudget", err)
	}
	if comparisons := m.tests * typesPerTest; comparisons > hideMatchBudget {
		t.Fatalf("%d tests of at least %d type comparisons each = %d comparisons, want at most the budget %d",
			m.tests, typesPerTest, comparisons, hideMatchBudget)
	}
	if limit := hideMatchBudget + 2*(typesPerTest+3); m.work > limit {
		t.Fatalf("work charged %d, want at most %d (the budget and one test)", m.work, limit)
	}
	t.Logf("%d tests, %d units charged, budget %d", m.tests, m.work, hideMatchBudget)

	// Through /proxy the request fails with 413.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	defer upstream.Close()
	p := newTestHandler(t, settingsWith(allowExample(DomainOptions{})), upstream)
	rec := doProxyHides(p, "http://example.com/page", selectors)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413 (body=%q)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "too large for the hide selectors") {
		t.Fatalf("body %q does not name the hide selectors", rec.Body.String())
	}
}

// TestHideSelectorsHideEachElementOnce covers CR5 hardening: an element that
// several hide selectors match gets the hide declaration once, so its style
// attribute does not grow with the number of selectors (20 selectors that
// match every element of a large page took over 250 MiB). An existing inline
// style is kept.
func TestHideSelectorsHideEachElementOnce(t *testing.T) {
	doc := `<html><body><p>a</p><p style="color:red">b</p><p style=" color:red; ">c</p>` +
		`<p style="display:none!important">d</p><div style="color:blue">e</div></body></html>`
	selectors := append(matchAllSelectors(maxHideSelectors-1), "p, div")
	out, err := rewriteHTML([]byte(doc), mustParseURL(t, examplePage), "text/html", selectors)
	if err != nil {
		t.Fatalf("rewriteHTML: %v", err)
	}
	var missing []string
	for _, want := range []string{
		`<p style="display:none!important">a</p>`,
		`<p style="color:red;display:none!important">b</p>`,
		`<p style="color:red;display:none!important">c</p>`,
		`<p style="display:none!important">d</p>`,
		`<div style="color:blue;display:none!important">e</div>`,
	} {
		if !bytes.Contains(out, []byte(want)) {
			missing = append(missing, want)
		}
	}
	if n := bytes.Count(out, []byte(hideStyleDecl)); n != 5 || len(missing) > 0 {
		t.Fatalf("%d hide declarations, want one for each of the 5 elements; missing %q", n, missing)
	}

	// On a large page every element has exactly one declaration.
	const items = 20000
	root := parseHideFixture(t, bareParagraphs(items))
	if err := applyHideSelectors(root, matchAllSelectors(maxHideSelectors)); err != nil {
		t.Fatalf("applyHideSelectors: %v", err)
	}
	var b bytes.Buffer
	if err := html.Render(&b, root); err != nil {
		t.Fatalf("render: %v", err)
	}
	if n := bytes.Count(b.Bytes(), []byte(`<p style="display:none!important"></p>`)); n != items {
		t.Fatalf("%d of %d elements hidden with one declaration", n, items)
	}
	if n := bytes.Count(b.Bytes(), []byte(hideStyleDecl)); n != items {
		t.Fatalf("%d hide declarations for %d elements", n, items)
	}
}

// TestHideSelectorSplitKeepsEscapedWhitespace covers CR5 hardening: a
// compound selector that ends with an escaped space or tab (`#a\ ` is the id
// "a ") keeps it, and the *|* universal selector before a combinator is read
// as cascadia reads it. These selectors apply as cascadia and the panel
// editor accept them, instead of being skipped.
func TestHideSelectorSplitKeepsEscapedWhitespace(t *testing.T) {
	root := parseHideFixture(t, `<html><body><div id="a "><b>1</b></div><div id="a&#9;"><b>2</b></div>`+
		`<section><i class="x">3</i></section></body></html>`)
	for _, sel := range []string{
		`#a\  b`, "#a\\\t b", "#a\\\t\tb", `#a\ >b`, `#a\  > b`, `#a\ /**/ b`, `#a\  b, i`, `i, #a\  b`,
		`#a\9  b`, `#a\9 > b`, `*|* b`, `body *|* > i`, `*|* > section *|*.x`, `section > *|*.x`,
	} {
		t.Run(sel, func(t *testing.T) {
			want := cascadia.MustCompile(sel).MatchAll(root)
			if len(want) == 0 {
				t.Fatalf("test precondition: cascadia matches nothing for %q", sel)
			}
			if msg := checkHideSelectors([]string{sel}); msg != "" {
				t.Fatalf("checkHideSelectors = %q, want accepted", msg)
			}
			parts, err := compileHideSelector(sel)
			if err != nil {
				t.Fatalf("compileHideSelector: %v", err)
			}
			got, err := (&hideMatcher{budget: hideMatchBudget}).match(root, parts)
			if err != nil {
				t.Fatalf("match: %v", err)
			}
			if !sameNodes(got, want) {
				t.Fatalf("matched %d elements, cascadia matches %d", len(got), len(want))
			}
		})
	}
}

// doProxyHides issues GET /proxy?url=<target> with one hide param per selector.
func doProxyHides(handler http.Handler, target string, selectors []string) *httptest.ResponseRecorder {
	q := url.Values{"url": {target}, hideParam: selectors}
	req := httptest.NewRequest(http.MethodGet, proxyPath+"?"+q.Encode(), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
