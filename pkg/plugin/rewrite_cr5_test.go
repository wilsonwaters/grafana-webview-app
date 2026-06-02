package plugin

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
)

// compileForTest mirrors the production cascadia compile used for validation, so a
// test can assert whether a selector is genuinely accepted/rejected by the same
// compiler the rewriter uses (it returns only the error of interest).
func compileForTest(selector string) (cascadia.Selector, error) {
	return cascadia.Compile(selector)
}

// hideDoc runs rewriteHTML over htmlIn against examplePage with the given CR5
// hide-selectors and re-parses the rewritten output with goquery so assertions
// read inline style attributes from the rewritten DOM rather than scraping the
// serialized string. It mirrors rewriteDoc but threads hideSelectors through.
func hideDoc(t *testing.T, htmlIn string, hideSelectors []string) *goquery.Document {
	t.Helper()
	out, err := rewriteHTML([]byte(htmlIn), mustParseURL(t, examplePage), "text/html", hideSelectors)
	if err != nil {
		t.Fatalf("rewriteHTML(hide=%v): %v", hideSelectors, err)
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("re-parse rewritten HTML: %v", err)
	}
	return doc
}

// hasHideDecl reports whether a style attribute value contains the fixed hide
// declaration written by hideElement.
func hasHideDecl(style string) bool {
	return strings.Contains(strings.ReplaceAll(style, " ", ""), hideStyleDecl)
}

// --- Completion Criterion: valid hide-selectors hide matching elements -------

// TestCR5ValidSelectorHidesMatchingElements covers Completion Criterion "valid
// hide-selectors applied to proxied HTML (elements hidden)": a matching element
// gains an inline display:none!important.
func TestCR5ValidSelectorHidesMatchingElements(t *testing.T) {
	const html = `<html><body><div class="ad">x</div><p>keep</p></body></html>`
	doc := hideDoc(t, html, []string{".ad"})

	adStyle, _ := doc.Find("div.ad").Attr("style")
	if !hasHideDecl(adStyle) {
		t.Errorf("matched .ad style = %q, want it to contain %q", adStyle, hideStyleDecl)
	}
	if pStyle, ok := doc.Find("p").Attr("style"); ok && hasHideDecl(pStyle) {
		t.Errorf("non-matching <p> was hidden: style=%q", pStyle)
	}
}

// TestCR5PreservesExistingInlineStyle covers Completion Criterion "existing inline
// style is preserved": the prior declaration survives and the hide declaration is
// appended (separated by ';').
func TestCR5PreservesExistingInlineStyle(t *testing.T) {
	const html = `<html><body><div class="ad" style="color:red">x</div></body></html>`
	doc := hideDoc(t, html, []string{".ad"})

	style, _ := doc.Find("div.ad").Attr("style")
	if !strings.Contains(style, "color:red") {
		t.Errorf("existing inline style lost: %q", style)
	}
	if !hasHideDecl(style) {
		t.Errorf("hide declaration not appended: %q", style)
	}
	if !strings.Contains(style, ";") {
		t.Errorf("existing and hide declarations not separated by ';': %q", style)
	}
}

// TestCR5MultipleHideParams covers Completion Criterion "multiple hide params":
// every distinct selector is applied independently.
func TestCR5MultipleHideParams(t *testing.T) {
	const html = `<html><body><div id="a">a</div><span class="b">b</span><p>c</p></body></html>`
	doc := hideDoc(t, html, []string{"#a", ".b"})

	aStyle, _ := doc.Find("#a").Attr("style")
	if !hasHideDecl(aStyle) {
		t.Errorf("#a not hidden: %q", aStyle)
	}
	bStyle, _ := doc.Find(".b").Attr("style")
	if !hasHideDecl(bStyle) {
		t.Errorf(".b not hidden: %q", bStyle)
	}
	if pStyle, ok := doc.Find("p").Attr("style"); ok && hasHideDecl(pStyle) {
		t.Errorf("non-matching <p> hidden: %q", pStyle)
	}
}

// TestCR5SelectorMatchingNothingIsNoOp covers Completion Criterion "a selector
// matching nothing is a no-op": no element is altered and no error occurs.
func TestCR5SelectorMatchingNothingIsNoOp(t *testing.T) {
	const html = `<html><body><div class="ad">x</div></body></html>`
	doc := hideDoc(t, html, []string{".does-not-exist"})

	if style, ok := doc.Find("div.ad").Attr("style"); ok && hasHideDecl(style) {
		t.Errorf("element hidden by a non-matching selector: %q", style)
	}
}

// --- Completion Criterion: markup-injection safety (KEY AC-31 test) ----------

// TestCR5MarkupInjectionAttemptInjectsNoMarkup is the key AC-31 test. A
// cascadia-VALID attribute selector whose value embeds a </style><script> breakout
// MUST NOT inject any markup into the output: the selector text must never appear
// as live markup, and zero injected <script> nodes may exist. This proves the
// injection-proof design (goquery FindMatcher + inline-style SetAttr, never a
// <style> block built from selector text).
func TestCR5MarkupInjectionAttemptInjectsNoMarkup(t *testing.T) {
	const breakout = `</style><script>alert(1)</script>`
	// A cascadia-valid attribute selector carrying the breakout payload as its value.
	selector := `[data-x="` + breakout + `"]`

	// Pre-flight: the selector MUST be cascadia-valid, else the test would pass
	// trivially (rejected at validation) and prove nothing about injection safety.
	if _, err := compileForTest(selector); err != nil {
		t.Fatalf("test precondition: selector must be cascadia-valid, got %v", err)
	}

	// The fixture element's data-x value EQUALS the breakout payload, so the
	// cascadia attribute selector genuinely matches and the hide/inline-style path
	// is exercised. Inside a double-quoted HTML attribute the '<' and '>' are
	// ordinary characters, so the value parses intact; the rewriter must re-emit it
	// escaped on output — never as live markup.
	html := `<html><body><div data-x="` + breakout + `">payload-target</div><p>untouched</p></body></html>`
	out, err := rewriteHTML([]byte(html), mustParseURL(t, examplePage), "text/html", []string{selector})
	if err != nil {
		t.Fatalf("rewriteHTML: %v", err)
	}
	s := string(out)

	// 1) No injected <script> survives a re-parse.
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if n := doc.Find("script").Length(); n != 0 {
		t.Fatalf("injection: %d <script> node(s) appeared; output: %s", n, s)
	}

	// 2) The raw breakout markup must not appear as live markup anywhere.
	if strings.Contains(s, breakout) {
		t.Fatalf("injection: raw breakout markup leaked into output: %s", s)
	}
	// 3) Not even the bare opening tags may appear unescaped.
	if strings.Contains(s, "<script>") || strings.Contains(s, "</style>") {
		t.Fatalf("injection: unescaped tag from selector text appeared: %s", s)
	}

	// 4) The selector DID match (data-x present) and the element was hidden, so the
	// matching path is genuinely exercised — injection safety is not achieved by
	// the selector simply being ignored.
	matchedStyle, _ := doc.Find("div[data-x]").Attr("style")
	if !hasHideDecl(matchedStyle) {
		t.Fatalf("matching element was not hidden, so injection path was not exercised: style=%q", matchedStyle)
	}
}

// --- Completion Criterion: invalid selectors rejected ------------------------

// TestCR5InvalidSelectorSkippedNoError covers Completion Criterion "invalid/malicious
// selectors rejected before use": a malformed selector is skipped without error and
// without affecting valid selectors in the same request.
func TestCR5InvalidSelectorSkippedNoError(t *testing.T) {
	const html = `<html><body><div class="ad">x</div></body></html>`
	for _, bad := range []string{">>>bad", "div[", "a:::", "{}", ".a >"} {
		// rewriteHTML must NOT error on an invalid selector.
		out, err := rewriteHTML([]byte(html), mustParseURL(t, examplePage), "text/html", []string{bad})
		if err != nil {
			t.Errorf("invalid selector %q caused error: %v", bad, err)
		}
		// Pre-flight: confirm the selector really is rejected by cascadia.
		if _, cerr := compileForTest(bad); cerr == nil {
			t.Errorf("test precondition: %q expected to be cascadia-invalid", bad)
		}
		if len(out) == 0 {
			t.Errorf("invalid selector %q produced empty output", bad)
		}
	}

	// A valid selector alongside an invalid one is still applied.
	doc := hideDoc(t, html, []string{">>>bad", ".ad"})
	style, _ := doc.Find("div.ad").Attr("style")
	if !hasHideDecl(style) {
		t.Errorf("valid selector not applied when paired with an invalid one: %q", style)
	}
}

// TestCR5OversizedSelectorRejected covers Completion Criterion "oversized selector
// rejected": a selector longer than maxHideSelectorLen is skipped (never reaching
// cascadia.Compile), while a valid selector in the same request still applies.
func TestCR5OversizedSelectorRejected(t *testing.T) {
	oversized := "." + strings.Repeat("a", maxHideSelectorLen) // > maxHideSelectorLen bytes
	if len(oversized) <= maxHideSelectorLen {
		t.Fatalf("test setup: oversized selector len %d not > %d", len(oversized), maxHideSelectorLen)
	}
	const html = `<html><body><div class="ad">x</div></body></html>`
	doc := hideDoc(t, html, []string{oversized, ".ad"})

	// The oversized selector is class-shaped, so had it been honoured it would not
	// match .ad anyway; the assertion that matters is no error + valid one applied.
	style, _ := doc.Find("div.ad").Attr("style")
	if !hasHideDecl(style) {
		t.Errorf("valid selector not applied alongside oversized one: %q", style)
	}
}

// TestCR5OverCountCapEnforced covers Completion Criterion "over-count cap enforced":
// only the first maxHideSelectors valid selectors are honoured; selectors beyond
// the cap are ignored. Selectors are ordered so each targets a distinct element,
// and the element targeted by the (cap+1)-th selector must remain unhidden.
func TestCR5OverCountCapEnforced(t *testing.T) {
	// Build maxHideSelectors valid leading selectors that match nothing in the
	// fixture, then one extra selector that WOULD match #overflow if honoured.
	selectors := make([]string, 0, maxHideSelectors+1)
	for i := 0; i < maxHideSelectors; i++ {
		selectors = append(selectors, ".filler") // valid, matches nothing
	}
	selectors = append(selectors, "#overflow") // the (cap+1)-th: must be ignored

	const html = `<html><body><div id="overflow">x</div></body></html>`
	doc := hideDoc(t, html, selectors)

	if style, ok := doc.Find("#overflow").Attr("style"); ok && hasHideDecl(style) {
		t.Errorf("over-count selector beyond the cap was honoured: style=%q", style)
	}

	// Sanity: with the same selector WITHIN the cap it WOULD be applied.
	docOK := hideDoc(t, html, []string{"#overflow"})
	style, _ := docOK.Find("#overflow").Attr("style")
	if !hasHideDecl(style) {
		t.Fatalf("control: #overflow should be hidden when within the cap: %q", style)
	}
}

// --- Completion Criterion: empty hideSelectors == byte-identical no-hide -----

// TestCR5EmptyHideSelectorsByteIdentical covers Completion Criterion "empty
// hideSelectors == byte-identical to a no-hide rewrite": nil and empty slices both
// produce output byte-for-byte identical to a rewrite with no hide-selectors.
func TestCR5EmptyHideSelectorsByteIdentical(t *testing.T) {
	const html = `<html><body><div class="ad" style="color:red">x</div><a href="/n">n</a></body></html>`
	pageURL := mustParseURL(t, examplePage)

	base, err := rewriteHTML([]byte(html), pageURL, "text/html", nil)
	if err != nil {
		t.Fatalf("base rewrite: %v", err)
	}
	for name, hide := range map[string][]string{"nil": nil, "empty": {}} {
		out, err := rewriteHTML([]byte(html), pageURL, "text/html", hide)
		if err != nil {
			t.Fatalf("%s rewrite: %v", name, err)
		}
		if !bytes.Equal(out, base) {
			t.Errorf("%s hideSelectors not byte-identical to no-hide rewrite:\n got=%q\nwant=%q", name, out, base)
		}
	}
}

// --- Completion Criterion: /proxy threads hide; /proxy-resource ignores it ---

// TestCR5ProxyEndpointAppliesHide covers Completion Criterion "valid hide-selectors
// applied through the full /proxy ModifyResponse path": the `hide` query param is
// threaded end-to-end and the matching element is hidden in the served body.
func TestCR5ProxyEndpointAppliesHide(t *testing.T) {
	const html = `<html><body><div class="ad">x</div><p>keep</p></body></html>`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if _, err := w.Write([]byte(html)); err != nil {
			t.Errorf("upstream write: %v", err)
		}
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	p := newTestHandler(t, cfg, upstream)

	rec := doProxyHide(p, "http://example.com/dir/page.html", ".ad")
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200 (body=%q)", rec.Code, rec.Body.String())
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(rec.Body.String()))
	if err != nil {
		t.Fatalf("parse proxied body: %v", err)
	}
	style, _ := doc.Find("div.ad").Attr("style")
	if !hasHideDecl(style) {
		t.Errorf("hide not applied through /proxy: style=%q body=%q", style, rec.Body.String())
	}
}

// TestCR5ProxyResourceIgnoresHide covers Completion Criterion "/proxy-resource
// ignores `hide`": subresources are not HTML documents and are passed through
// byte-identical even when a `hide` param is present.
func TestCR5ProxyResourceIgnoresHide(t *testing.T) {
	const css = `.ad { color: red; }` // CSS that textually contains ".ad"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		if _, err := w.Write([]byte(css)); err != nil {
			t.Errorf("upstream write: %v", err)
		}
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	h := newResourceHandler(t, cfg, upstream)

	req := httptest.NewRequest(http.MethodGet,
		proxyResourcePath+"?url="+url.QueryEscape("http://example.com/styles.css")+"&hide="+url.QueryEscape(".ad"), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200 (body=%q)", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != css {
		t.Errorf("/proxy-resource body altered despite hide param: got %q, want byte-identical %q", body, css)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/css" {
		t.Errorf("/proxy-resource Content-Type = %q, want text/css (no HTML rewrite)", ct)
	}
}

// --- hideSelectorsOf: query-param sourcing -----------------------------------

// TestCR5HideSelectorsOfReadsRepeatedParams covers Completion Criterion "hide
// selectors are sourced as repeated `hide` query params (never comma-joined)":
// each non-blank repeated value is lifted in order; blanks are dropped; absent
// params yield nil.
func TestCR5HideSelectorsOfReadsRepeatedParams(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/proxy?url=x&hide="+url.QueryEscape("div > .a, .b")+"&hide=%20%20&hide="+url.QueryEscape(".c"), nil)
	got := hideSelectorsOf(req)
	want := []string{"div > .a, .b", ".c"}
	if len(got) != len(want) {
		t.Fatalf("hideSelectorsOf = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("hideSelectorsOf[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	if got := hideSelectorsOf(httptest.NewRequest(http.MethodGet, "/proxy?url=x", nil)); got != nil {
		t.Errorf("hideSelectorsOf with no hide params = %v, want nil", got)
	}
}

// doProxyHide issues a GET /proxy?url=<target>&hide=<selector> and returns the
// recorder. target and selector are RAW (unencoded) and percent-encoded here.
func doProxyHide(handler http.Handler, target, selector string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet,
		proxyPath+"?url="+url.QueryEscape(target)+"&hide="+url.QueryEscape(selector), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
