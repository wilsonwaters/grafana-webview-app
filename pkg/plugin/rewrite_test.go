package plugin

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

// nopBody wraps a string as a response body reader.
func nopBody(s string) io.ReadCloser { return io.NopCloser(strings.NewReader(s)) }

// readAllString drains resp.Body and returns it as a string.
func readAllString(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// itoa is a tiny strconv.Itoa alias for terse Content-Length assertions.
func itoa(n int) string { return strconv.Itoa(n) }

// mustParseURL parses a raw URL for a test fixture, failing the test on error.
func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse URL %q: %v", raw, err)
	}
	return u
}

// rewriteDoc runs rewriteHTML over htmlIn against pageURL and re-parses the
// result with goquery so assertions read attribute values from the rewritten DOM
// rather than scraping the serialized string.
func rewriteDoc(t *testing.T, htmlIn, pageURL string) *goquery.Document {
	t.Helper()
	out, err := rewriteHTML([]byte(htmlIn), mustParseURL(t, pageURL), "text/html")
	if err != nil {
		t.Fatalf("rewriteHTML(%q): %v", htmlIn, err)
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("re-parse rewritten HTML: %v", err)
	}
	return doc
}

// attr fetches the named attribute of the first element matching selector.
func attr(t *testing.T, doc *goquery.Document, selector, name string) string {
	t.Helper()
	v, _ := doc.Find(selector).First().Attr(name)
	return v
}

const examplePage = "http://example.com/dir/page.html"

// wantResource is the /proxy-resource URL for an absolute upstream URL.
func wantResource(target string) string {
	return proxyResourceURL(target)
}

// wantNav is the /proxy URL for an absolute upstream URL.
func wantNav(target string) string {
	return proxyNavURL(target)
}

// --- Completion Criterion: subresource URL rewriting (every URL form) ---------

// TestRewriteSubresourceURLForms covers Completion Criterion "subresource src/href
// rewritten to /proxy-resource" across absolute, root-relative, path-relative,
// protocol-relative and query/fragment-bearing forms.
func TestRewriteSubresourceURLForms(t *testing.T) {
	cases := []struct {
		name string
		in   string // img src value
		want string // absolute upstream URL we expect to be query-encoded
	}{
		{"absolute-http", "http://cdn.example.com/a.png", "http://cdn.example.com/a.png"},
		{"absolute-https", "https://cdn.example.com/a.png", "https://cdn.example.com/a.png"},
		{"root-relative", "/img/a.png", "http://example.com/img/a.png"},
		{"path-relative", "a.png", "http://example.com/dir/a.png"},
		{"dotdot-relative", "../up.png", "http://example.com/up.png"},
		{"protocol-relative", "//cdn.example.com/a.png", "http://cdn.example.com/a.png"},
		{"query-preserved", "/a.png?v=1&w=2", "http://example.com/a.png?v=1&w=2"},
		{"fragment-stripped", "/a.png#frag", "http://example.com/a.png"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := rewriteDoc(t, `<img src="`+c.in+`">`, examplePage)
			got := attr(t, doc, "img", "src")
			if got != wantResource(c.want) {
				t.Errorf("img src = %q, want %q", got, wantResource(c.want))
			}
		})
	}
}

// TestRewriteSubresourceAttributeSet covers Completion Criterion "the full
// subresource attribute set routes through /proxy-resource".
func TestRewriteSubresourceAttributeSet(t *testing.T) {
	cases := []struct {
		name     string
		html     string
		selector string
		attr     string
	}{
		{"img-src", `<img src="/a.png">`, "img", "src"},
		{"script-src", `<script src="/a.js"></script>`, "script", "src"},
		{"source-src", `<video><source src="/a.mp4"></video>`, "source", "src"},
		{"video-src", `<video src="/a.mp4"></video>`, "video", "src"},
		{"audio-src", `<audio src="/a.mp3"></audio>`, "audio", "src"},
		{"track-src", `<video><track src="/a.vtt"></video>`, "track", "src"},
		{"object-data", `<object data="/a.swf"></object>`, "object", "data"},
		{"embed-src", `<embed src="/a.swf">`, "embed", "src"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := rewriteDoc(t, c.html, examplePage)
			got := attr(t, doc, c.selector, c.attr)
			if !strings.HasPrefix(got, resourceBase+proxyResourcePath+"?url=") {
				t.Errorf("%s %s = %q, want /proxy-resource URL", c.selector, c.attr, got)
			}
		})
	}
}

// TestRewriteNavigationAttributeSet covers Completion Criterion "navigation refs
// route through /proxy (re-enter top-level rewriting)".
func TestRewriteNavigationAttributeSet(t *testing.T) {
	cases := []struct {
		name     string
		html     string
		selector string
		attr     string
	}{
		{"a-href", `<a href="/p.html">x</a>`, "a", "href"},
		{"area-href", `<map><area href="/p.html"></map>`, "area", "href"},
		{"iframe-src", `<iframe src="/p.html"></iframe>`, "iframe", "src"},
		{"form-action-get", `<form action="/submit" method="get"></form>`, "form", "action"},
		{"form-action-nomethod", `<form action="/submit"></form>`, "form", "action"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := rewriteDoc(t, c.html, examplePage)
			got := attr(t, doc, c.selector, c.attr)
			if !strings.HasPrefix(got, resourceBase+proxyPath+"?url=") {
				t.Errorf("%s %s = %q, want /proxy URL", c.selector, c.attr, got)
			}
		})
	}
}

// TestRewritePostFormLeftVerbatim covers Completion Criterion "POST forms are NOT
// rewritten" (out of scope — only GET/absent-method forms).
func TestRewritePostFormLeftVerbatim(t *testing.T) {
	doc := rewriteDoc(t, `<form action="/submit" method="post"></form>`, examplePage)
	if got := attr(t, doc, "form", "action"); got != "/submit" {
		t.Errorf("POST form action = %q, want untouched /submit", got)
	}
	// Case-insensitive: POST in any case is left.
	doc2 := rewriteDoc(t, `<form action="/submit" method="POST"></form>`, examplePage)
	if got := attr(t, doc2, "form", "action"); got != "/submit" {
		t.Errorf("POST (upper) form action = %q, want untouched", got)
	}
}

// TestRewriteNavFragmentPreserved covers Completion Criterion "a trailing #frag on
// a navigation URL is preserved outside the url= param".
func TestRewriteNavFragmentPreserved(t *testing.T) {
	doc := rewriteDoc(t, `<a href="/p.html#section2">x</a>`, examplePage)
	got := attr(t, doc, "a", "href")
	want := wantNav("http://example.com/p.html") + "#section2"
	if got != want {
		t.Errorf("nav href = %q, want %q", got, want)
	}
}

// TestRewriteLinkRelGating covers Completion Criterion "link[href] rewritten only
// for rewritable rels (or rel absent); other rels left verbatim".
func TestRewriteLinkRelGating(t *testing.T) {
	rewritten := []string{"stylesheet", "icon", "shortcut icon", "apple-touch-icon", "mask-icon", "preload", "modulepreload", "manifest"}
	for _, rel := range rewritten {
		doc := rewriteDoc(t, `<link rel="`+rel+`" href="/r.css">`, examplePage)
		got := attr(t, doc, "link", "href")
		if !strings.HasPrefix(got, resourceBase+proxyResourcePath+"?url=") {
			t.Errorf("link rel=%q href = %q, want rewritten", rel, got)
		}
	}
	// rel absent ⇒ rewritten.
	docAbsent := rewriteDoc(t, `<link href="/r.css">`, examplePage)
	if got := attr(t, docAbsent, "link", "href"); !strings.HasPrefix(got, resourceBase+proxyResourcePath+"?url=") {
		t.Errorf("link rel-absent href = %q, want rewritten", got)
	}
	// Non-rewritable rels left verbatim.
	for _, rel := range []string{"canonical", "alternate", "dns-prefetch", "preconnect"} {
		doc := rewriteDoc(t, `<link rel="`+rel+`" href="/r">`, examplePage)
		if got := attr(t, doc, "link", "href"); got != "/r" {
			t.Errorf("link rel=%q href = %q, want left verbatim", rel, got)
		}
	}
}

// TestRewriteNonHTTPSchemesVerbatim covers Completion Criterion "non-http(s) and
// empty/fragment refs are left verbatim".
func TestRewriteNonHTTPSchemesVerbatim(t *testing.T) {
	cases := []string{
		"data:image/png;base64,AAAA",
		"blob:http://example.com/uuid",
		"mailto:a@b.com",
		"tel:+123",
		"javascript:void(0)",
		"about:blank",
		"#fragment",
		"",
	}
	for _, ref := range cases {
		doc := rewriteDoc(t, `<a href="`+ref+`">x</a>`, examplePage)
		if got := attr(t, doc, "a", "href"); got != ref {
			t.Errorf("a href=%q rewritten to %q, want verbatim", ref, got)
		}
	}
}

// TestRewriteSrcset covers Completion Criterion "srcset candidates rewritten,
// descriptors preserved, non-rewritable URLs kept".
func TestRewriteSrcset(t *testing.T) {
	doc := rewriteDoc(t, `<img srcset="/a.png 1x, /b.png 2x, c.png 640w">`, examplePage)
	got := attr(t, doc, "img", "srcset")
	wantParts := []string{
		wantResource("http://example.com/a.png") + " 1x",
		wantResource("http://example.com/b.png") + " 2x",
		wantResource("http://example.com/dir/c.png") + " 640w",
	}
	if got != strings.Join(wantParts, ", ") {
		t.Errorf("srcset = %q, want %q", got, strings.Join(wantParts, ", "))
	}

	// A non-rewritable (about:) candidate is kept verbatim; http candidate
	// alongside it is still rewritten. (srcset URLs cannot contain bare commas per
	// the HTML spec, so a comma-bearing data: URI is out of scope.)
	doc2 := rewriteDoc(t, `<img srcset="about:blank 1x, /b.png 2x">`, examplePage)
	got2 := attr(t, doc2, "img", "srcset")
	if !strings.Contains(got2, "about:blank 1x") {
		t.Errorf("srcset non-rewritable candidate not preserved: %q", got2)
	}
	if !strings.Contains(got2, wantResource("http://example.com/b.png")+" 2x") {
		t.Errorf("srcset http candidate not rewritten: %q", got2)
	}

	// source[srcset] also handled.
	doc3 := rewriteDoc(t, `<picture><source srcset="/s.png 1x"></picture>`, examplePage)
	if got := attr(t, doc3, "source", "srcset"); !strings.HasPrefix(got, resourceBase+proxyResourcePath) {
		t.Errorf("source srcset = %q, want rewritten", got)
	}
}

// --- Completion Criterion: base href inject + fix ----------------------------

// TestBaseInjectedWhenAbsent covers Completion Criterion "a <base href> is
// injected as the first head child when none exists".
func TestBaseInjectedWhenAbsent(t *testing.T) {
	doc := rewriteDoc(t, `<html><head><title>t</title></head><body></body></html>`, examplePage)
	base := doc.Find("head base").First()
	if base.Length() == 0 {
		t.Fatal("no <base> injected")
	}
	if href, _ := base.Attr("href"); href != examplePage {
		t.Errorf("injected base href = %q, want %q", href, examplePage)
	}
	// It must be the FIRST child of head.
	if first := doc.Find("head").Children().First(); goquery.NodeName(first) != "base" {
		t.Errorf("first head child = %q, want base", goquery.NodeName(first))
	}
}

// TestBaseFixedWhenPresent covers Completion Criterion "an existing relative
// <base href> is resolved to absolute against the page URL".
func TestBaseFixedWhenPresent(t *testing.T) {
	doc := rewriteDoc(t, `<html><head><base href="/app/"><title>t</title></head></html>`, examplePage)
	bases := doc.Find("base")
	if bases.Length() != 1 {
		t.Fatalf("expected exactly one base, got %d", bases.Length())
	}
	if href, _ := bases.First().Attr("href"); href != "http://example.com/app/" {
		t.Errorf("fixed base href = %q, want absolute http://example.com/app/", href)
	}
}

// TestBaseAffectsRelativeResolution covers Completion Criterion "refs resolve
// against the effective base (page URL combined with existing <base>)".
func TestBaseAffectsRelativeResolution(t *testing.T) {
	doc := rewriteDoc(t, `<html><head><base href="http://other.com/x/"></head><body><img src="a.png"></body></html>`, examplePage)
	got := attr(t, doc, "img", "src")
	if got != wantResource("http://other.com/x/a.png") {
		t.Errorf("img src = %q, want resolved against existing base", got)
	}
}

// --- Completion Criterion: meta removal --------------------------------------

// TestRemoveCSPAndRefreshMetas covers Completion Criterion "CSP and refresh meta
// tags removed; charset/viewport/XFO-meta preserved".
func TestRemoveCSPAndRefreshMetas(t *testing.T) {
	in := `<html><head>` +
		`<meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width">` +
		`<meta http-equiv="Content-Security-Policy" content="default-src 'none'">` +
		`<meta http-equiv="content-security-policy-report-only" content="default-src 'none'">` +
		`<meta http-equiv="refresh" content="5;url=http://evil.com">` +
		`<meta http-equiv="X-Frame-Options" content="DENY">` +
		`</head></html>`
	doc := rewriteDoc(t, in, examplePage)

	if doc.Find(`meta[http-equiv]`).FilterFunction(func(_ int, s *goquery.Selection) bool {
		v, _ := s.Attr("http-equiv")
		return strings.EqualFold(v, "content-security-policy") ||
			strings.EqualFold(v, "content-security-policy-report-only") ||
			strings.EqualFold(v, "refresh")
	}).Length() != 0 {
		t.Error("CSP/refresh meta not removed")
	}
	// charset + viewport preserved.
	if doc.Find(`meta[charset]`).Length() != 1 {
		t.Error("charset meta should be preserved")
	}
	if doc.Find(`meta[name="viewport"]`).Length() != 1 {
		t.Error("viewport meta should be preserved")
	}
	// X-Frame-Options meta is inert and left in place.
	if doc.Find(`meta[http-equiv="X-Frame-Options"]`).Length() != 1 {
		t.Error("X-Frame-Options meta should be left (inert)")
	}
}

// --- Completion Criterion: frame-buster removal ------------------------------

// TestRemoveFrameBusterFull covers Completion Criterion "an inline frame-buster
// with BOTH a comparison and a navigation marker is removed".
func TestRemoveFrameBusterFull(t *testing.T) {
	cases := []string{
		`if (top != self) { top.location = self.location; }`,
		`if (window.top !== window.self) window.top.location.replace(location.href);`,
		`if (parent.frames.length > 0) parent.location.href = document.location.href;`,
		`if(window.frameElement){ top.location.href = window.location.href; }`,
	}
	for _, js := range cases {
		doc := rewriteDoc(t, `<html><body><script>`+js+`</script></body></html>`, examplePage)
		if doc.Find("script").Length() != 0 {
			t.Errorf("frame-buster not removed: %q", js)
		}
	}
}

// TestRemoveFrameBusterSingleMarkerKept covers Completion Criterion "a script with
// only ONE marker (comparison OR navigation, not both) is KEPT (false-negative
// bias)".
func TestRemoveFrameBusterSingleMarkerKept(t *testing.T) {
	cases := []struct {
		name string
		js   string
	}{
		{"comparison-only", `if (top != self) { console.log("framed"); }`},
		{"navigation-only", `document.getElementById("go").onclick = function(){ top.location = "/home"; };`},
		{"neither", `console.log("hello world");`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := rewriteDoc(t, `<html><body><script>`+c.js+`</script></body></html>`, examplePage)
			if doc.Find("script").Length() != 1 {
				t.Errorf("script should be KEPT (only partial markers): %q", c.js)
			}
		})
	}
}

// TestFrameBusterExternalScriptKept covers Completion Criterion "external scripts
// (with src) are never scanned/removed; their body is not fetched".
func TestFrameBusterExternalScriptKept(t *testing.T) {
	// An external script whose src happens to point at a buster-named file, plus
	// the marker text in an attribute that we should NOT scan, must survive.
	doc := rewriteDoc(t, `<html><body><script src="/buster.js"></script></body></html>`, examplePage)
	if doc.Find("script").Length() != 1 {
		t.Fatal("external script should be kept")
	}
	// And its src must have been rewritten to /proxy-resource (subresource).
	if got := attr(t, doc, "script", "src"); !strings.HasPrefix(got, resourceBase+proxyResourcePath) {
		t.Errorf("external script src = %q, want rewritten subresource URL", got)
	}
}

// TestFrameBusterLdJSONUntouched covers Completion Criterion "a non-executable
// script type (application/ld+json) is never removed even if it contains marker
// substrings".
func TestFrameBusterLdJSONUntouched(t *testing.T) {
	js := `{"note":"if (top != self) top.location = self.location"}`
	doc := rewriteDoc(t, `<html><body><script type="application/ld+json">`+js+`</script></body></html>`, examplePage)
	if doc.Find("script").Length() != 1 {
		t.Error("ld+json data block must not be removed")
	}
}

// TestFrameBusterModuleAndTextJavascriptScanned confirms type="module" and
// type="text/javascript" inline scripts ARE scanned.
func TestFrameBusterModuleAndTextJavascriptScanned(t *testing.T) {
	for _, typ := range []string{"module", "text/javascript", "application/javascript"} {
		js := `if (top != self) { top.location = self.location; }`
		doc := rewriteDoc(t, `<html><body><script type="`+typ+`">`+js+`</script></body></html>`, examplePage)
		if doc.Find("script").Length() != 0 {
			t.Errorf("inline frame-buster with type=%q should be removed", typ)
		}
	}
}

// --- Completion Criterion: security / XSS ------------------------------------

// TestRewriteEscapesHostileURL covers Completion Criterion "a crafted upstream URL
// containing \"><script> ends up escaped inside the attribute (no breakout)".
func TestRewriteEscapesHostileURL(t *testing.T) {
	hostile := `/p"><script>alert(1)</script>`
	out, err := rewriteHTML([]byte(`<a href='`+hostile+`'>x</a>`), mustParseURL(t, examplePage), "text/html")
	if err != nil {
		t.Fatalf("rewriteHTML: %v", err)
	}
	s := string(out)
	// The raw breakout sequence must NOT appear literally in the output.
	if strings.Contains(s, `"><script>alert(1)</script>`) {
		t.Fatalf("hostile URL broke out of the attribute: %s", s)
	}
	// Re-parsing must yield exactly one <a> and zero injected <script> elements.
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if doc.Find("script").Length() != 0 {
		t.Fatalf("an injected <script> survived; output: %s", s)
	}
	href := attr(t, doc, "a", "href")
	if !strings.HasPrefix(href, resourceBase+proxyPath+"?url=") {
		t.Fatalf("a href = %q, want proxied + encoded", href)
	}
}

// --- Completion Criterion: charset -------------------------------------------

// TestRewriteCharsetAware covers Completion Criterion "non-UTF-8 input is decoded
// via x/net/html/charset and emitted as UTF-8".
func TestRewriteCharsetAware(t *testing.T) {
	// "café" in ISO-8859-1: é is 0xE9.
	body := []byte("<html><head><meta charset=\"ISO-8859-1\"></head><body>caf\xe9</body></html>")
	out, err := rewriteHTML(body, mustParseURL(t, examplePage), "text/html; charset=ISO-8859-1")
	if err != nil {
		t.Fatalf("rewriteHTML: %v", err)
	}
	if !bytes.Contains(out, []byte("café")) {
		t.Errorf("expected UTF-8 'café' in output, got %q", string(out))
	}
}

// --- Completion Criterion: malformed HTML no panic ---------------------------

// TestRewriteMalformedNoPanic covers Completion Criterion "malformed HTML does not
// panic; rewriting still produces output".
func TestRewriteMalformedNoPanic(t *testing.T) {
	cases := []string{
		`<html><body><img src="/a.png"`,       // unclosed tag
		`<a href="/x"><b><i>unbalanced</a>`,   // mis-nested
		`plain text no tags`,                  // no markup
		`<<<>>><img src=/a.png>`,              // garbage + bare attr
		`<!doctype html><HtMl><BoDy>x</BoDy>`, // mixed case, missing close
	}
	for _, in := range cases {
		out, err := rewriteHTML([]byte(in), mustParseURL(t, examplePage), "text/html")
		if err != nil {
			t.Errorf("rewriteHTML(%q) errored: %v", in, err)
		}
		if len(out) == 0 {
			t.Errorf("rewriteHTML(%q) produced empty output", in)
		}
	}
	// Empty input is returned unchanged (no parse, no error).
	if out, err := rewriteHTML([]byte(``), mustParseURL(t, examplePage), "text/html"); err != nil || len(out) != 0 {
		t.Errorf("rewriteHTML(empty) = (%q, %v), want ([], nil)", string(out), err)
	}
}

// --- Integration through ModifyResponse / prepareHTMLBody --------------------

// TestRewriteThroughProxyIntegration covers Completion Criterion "rewriting runs
// on plain (non-gzip) HTML through the full ModifyResponse path".
func TestRewriteThroughProxyIntegration(t *testing.T) {
	const html = `<html><head><title>t</title></head><body><img src="/logo.png"><a href="/next">go</a></body></html>`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if _, err := w.Write([]byte(html)); err != nil {
			t.Errorf("upstream write: %v", err)
		}
	}))
	defer upstream.Close()

	cfg := settingsWith(allowExample(DomainOptions{}))
	p := newTestHandler(t, cfg, upstream)
	rec := doProxy(p, "http://example.com/dir/page.html")
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200 (body=%q)", rec.Code, rec.Body.String())
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(rec.Body.String()))
	if err != nil {
		t.Fatalf("parse proxied body: %v", err)
	}
	if got, _ := doc.Find("img").Attr("src"); !strings.HasPrefix(got, resourceBase+proxyResourcePath) {
		t.Errorf("img not rewritten through proxy: %q", got)
	}
	if got, _ := doc.Find("a").Attr("href"); !strings.HasPrefix(got, resourceBase+proxyPath) {
		t.Errorf("a not rewritten through proxy: %q", got)
	}
	if doc.Find("head base").Length() == 0 {
		t.Error("base not injected through proxy")
	}
}

// TestRewriteContentLengthUpdated covers Completion Criterion "Content-Length is
// updated to the rewritten length through ModifyResponse".
func TestRewriteContentLengthUpdated(t *testing.T) {
	const html = `<html><body><img src="/a.png"></body></html>`
	resp := &http.Response{Header: http.Header{}, Body: nopBody(html)}
	resp.Header.Set("Content-Type", "text/html")
	if err := prepareHTMLBody(resp, 1<<20, mustParseURL(t, examplePage), nil); err != nil {
		t.Fatalf("prepareHTMLBody: %v", err)
	}
	body := readAllString(t, resp)
	if resp.Header.Get("Content-Length") != itoa(len(body)) {
		t.Errorf("Content-Length header = %q, want %d", resp.Header.Get("Content-Length"), len(body))
	}
	if resp.ContentLength != int64(len(body)) {
		t.Errorf("resp.ContentLength = %d, want %d", resp.ContentLength, len(body))
	}
	if !strings.Contains(body, resourceBase+proxyResourcePath) {
		t.Errorf("body not rewritten: %q", body)
	}
}

// TestNonHTMLByteIdenticalPassthrough covers Completion Criterion "non-HTML
// responses pass through byte-identical (regression)". A non-HTML body must NOT
// be rewritten even if it looks like HTML.
func TestNonHTMLByteIdenticalPassthrough(t *testing.T) {
	const payload = `<html><body><img src="/a.png"></body></html>` // looks like HTML, but JSON content-type
	resp := &http.Response{Header: http.Header{}, Body: nopBody(payload)}
	resp.Header.Set("Content-Type", "application/json")
	resp.ContentLength = int64(len(payload))
	if err := prepareHTMLBody(resp, 1<<20, mustParseURL(t, examplePage), nil); err != nil {
		t.Fatalf("prepareHTMLBody: %v", err)
	}
	if body := readAllString(t, resp); body != payload {
		t.Errorf("non-HTML body modified: got %q, want byte-identical %q", body, payload)
	}
}

// TestRewriteFailureDegradesToOriginal covers Completion Criterion "a rewriteHTML
// failure serves the DECODED ORIGINAL HTML (status 200), NOT a 502". It swaps in
// a deterministically-failing rewriter via the htmlRewriter seam, drives
// prepareHTMLBody, and asserts: no error is returned (so the ReverseProxy serves
// 200), the ORIGINAL body bytes are preserved verbatim, and Content-Length
// matches the original length.
func TestRewriteFailureDegradesToOriginal(t *testing.T) {
	orig := htmlRewriter
	t.Cleanup(func() { htmlRewriter = orig })
	htmlRewriter = func(_ []byte, _ *url.URL, _ string) ([]byte, error) {
		return nil, errInjectedRewriteFailure
	}

	const html = `<html><body><img src="/a.png">original</body></html>`
	resp := &http.Response{Header: http.Header{}, Body: nopBody(html)}
	resp.Header.Set("Content-Type", "text/html")

	if err := prepareHTMLBody(resp, 1<<20, mustParseURL(t, examplePage), nil); err != nil {
		t.Fatalf("prepareHTMLBody must NOT return an error on rewrite failure (degrade): %v", err)
	}
	body := readAllString(t, resp)
	if body != html {
		t.Errorf("degraded body = %q, want the DECODED ORIGINAL %q", body, html)
	}
	if resp.Header.Get("Content-Length") != itoa(len(html)) {
		t.Errorf("Content-Length = %q, want %d", resp.Header.Get("Content-Length"), len(html))
	}
}

// errInjectedRewriteFailure is the deterministic error a test rewriter returns to
// exercise the degradation path.
var errInjectedRewriteFailure = errorString("injected rewrite failure")

type errorString string

func (e errorString) Error() string { return string(e) }
