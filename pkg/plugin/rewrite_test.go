package plugin

import (
	"bytes"
	"errors"
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
	out, err := rewriteHTML([]byte(htmlIn), mustParseURL(t, pageURL), "text/html", nil)
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

// isShim reports whether s is the injected sandbox compatibility script.
func isShim(s *goquery.Selection) bool { return s.Text() == sandboxCompatShim }

// pageScripts returns the document's <script> elements other than the
// injected sandbox compatibility shim, i.e. the scripts that came from the
// upstream page.
func pageScripts(doc *goquery.Document) *goquery.Selection {
	return doc.Find("script").FilterFunction(func(_ int, s *goquery.Selection) bool { return !isShim(s) })
}

// injectedShimMarkup is the exact markup the rewriter injects for the shim.
const injectedShimMarkup = "<script>" + sandboxCompatShim + "</script>"

// withoutInjectedShim returns the rendered HTML with the one injected shim
// element removed, so string assertions about upstream scripts ignore it.
func withoutInjectedShim(s string) string {
	return strings.Replace(s, injectedShimMarkup, "", 1)
}

const examplePage = "http://example.com/dir/page.html"

// --- FR5: URL attributes become absolute upstream URLs ------------------------

// TestRewriteSubresourceURLForms covers FR5 / R1 D1: a subresource URL in any
// form is rewritten to the absolute upstream URL, resolved against the page URL.
func TestRewriteSubresourceURLForms(t *testing.T) {
	cases := []struct {
		name string
		in   string // img src value
		want string // absolute upstream URL
	}{
		{"absolute-http", "http://cdn.example.com/a.png", "http://cdn.example.com/a.png"},
		{"absolute-https", "https://cdn.example.com/a.png", "https://cdn.example.com/a.png"},
		{"root-relative", "/img/a.png", "http://example.com/img/a.png"},
		{"path-relative", "a.png", "http://example.com/dir/a.png"},
		{"dot-relative", "./a.png", "http://example.com/dir/a.png"},
		{"dotdot-relative", "../up.png", "http://example.com/up.png"},
		{"dotdot-past-root", "../../../up.png", "http://example.com/up.png"},
		{"protocol-relative", "//cdn.example.com/a.png", "http://cdn.example.com/a.png"},
		{"query-only", "?v=2", "http://example.com/dir/page.html?v=2"},
		{"query-preserved", "/a.png?v=1&w=2", "http://example.com/a.png?v=1&w=2"},
		{"fragment-kept", "/sprite.svg#icon", "http://example.com/sprite.svg#icon"},
		{"surrounding-whitespace", "  /a.png  ", "http://example.com/a.png"},
		{"upper-case-scheme", "HTTPS://cdn.example.com/a.png", "https://cdn.example.com/a.png"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := rewriteDoc(t, `<img src="`+c.in+`">`, examplePage)
			if got := attr(t, doc, "img", "src"); got != c.want {
				t.Errorf("img src %q => %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestRewriteProtocolRelativeFollowsPageScheme confirms a protocol-relative
// URL takes the scheme of the (final) page URL.
func TestRewriteProtocolRelativeFollowsPageScheme(t *testing.T) {
	doc := rewriteDoc(t, `<script src="//cdn.example.com/a.js"></script>`, "https://example.com/p")
	if got := attr(t, doc, "script[src]", "src"); got != "https://cdn.example.com/a.js" {
		t.Errorf("script src = %q, want https://cdn.example.com/a.js", got)
	}
}

// TestRewriteURLAttributeSet covers FR5: every subresource and navigation
// attribute in urlAttrs becomes an absolute upstream URL (no /proxy or
// /proxy-resource paths, R1 D1 and D4).
func TestRewriteURLAttributeSet(t *testing.T) {
	cases := []struct {
		name     string
		html     string
		selector string
		attr     string
		want     string
	}{
		// Subresources.
		{"img-src", `<img src="a.png">`, "img", "src", "http://example.com/dir/a.png"},
		{"script-src", `<script src="/a.js"></script>`, "script[src]", "src", "http://example.com/a.js"},
		{"link-stylesheet", `<link rel="stylesheet" href="s.css">`, "link", "href", "http://example.com/dir/s.css"},
		{"source-src", `<video><source src="/a.mp4"></video>`, "source", "src", "http://example.com/a.mp4"},
		{"video-src", `<video src="/a.mp4"></video>`, "video", "src", "http://example.com/a.mp4"},
		{"video-poster", `<video poster="p.jpg"></video>`, "video", "poster", "http://example.com/dir/p.jpg"},
		{"audio-src", `<audio src="/a.mp3"></audio>`, "audio", "src", "http://example.com/a.mp3"},
		{"track-src", `<video><track src="/a.vtt"></video>`, "track", "src", "http://example.com/a.vtt"},
		{"object-data", `<object data="/a.svg"></object>`, "object", "data", "http://example.com/a.svg"},
		{"embed-src", `<embed src="/a.svg">`, "embed", "src", "http://example.com/a.svg"},
		{"input-image-src", `<form><input type="image" src="go.png"></form>`, "input", "src", "http://example.com/dir/go.png"},
		{"body-background", `<body background="bg.gif"></body>`, "body", "background", "http://example.com/dir/bg.gif"},
		{"td-background", `<table><tr><td background="../c.gif">x</td></tr></table>`, "td", "background", "http://example.com/c.gif"},
		// Navigation targets.
		{"a-href", `<a href="/p.html">x</a>`, "a", "href", "http://example.com/p.html"},
		{"area-href", `<map><area href="p.html"></map>`, "area", "href", "http://example.com/dir/p.html"},
		{"iframe-src", `<iframe src="/frame.html"></iframe>`, "iframe", "src", "http://example.com/frame.html"},
		{"frame-src", `<frameset><frame src="f.html"></frameset>`, "frame", "src", "http://example.com/dir/f.html"},
		{"form-action-get", `<form action="/submit" method="get"></form>`, "form", "action", "http://example.com/submit"},
		{"form-action-nomethod", `<form action="submit"></form>`, "form", "action", "http://example.com/dir/submit"},
		{"form-action-post", `<form action="/submit" method="post"></form>`, "form", "action", "http://example.com/submit"},
		{"button-formaction", `<form><button formaction="/b">x</button></form>`, "button", "formaction", "http://example.com/b"},
		{"input-formaction", `<form><input type="submit" formaction="/i"></form>`, "input", "formaction", "http://example.com/i"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := rewriteDoc(t, c.html, examplePage)
			got := attr(t, doc, c.selector, c.attr)
			if got != c.want {
				t.Errorf("%s[%s] = %q, want %q", c.selector, c.attr, got, c.want)
			}
			if strings.Contains(got, "/api/plugins/") {
				t.Errorf("%s[%s] = %q still routes through Grafana", c.selector, c.attr, got)
			}
		})
	}
}

// TestRewriteNavFragmentPreserved covers FR5: a #fragment on a navigation URL
// stays on the absolute URL.
func TestRewriteNavFragmentPreserved(t *testing.T) {
	doc := rewriteDoc(t, `<a href="/p.html#section2">x</a>`, examplePage)
	if got := attr(t, doc, "a", "href"); got != "http://example.com/p.html#section2" {
		t.Errorf("nav href = %q, want http://example.com/p.html#section2", got)
	}
}

// TestRewriteLinkHrefAnyRel covers FR5: with absolute URLs there is no reason to
// gate <link> on rel, so every link href is absolute.
func TestRewriteLinkHrefAnyRel(t *testing.T) {
	for _, rel := range []string{"stylesheet", "icon", "preload", "modulepreload", "manifest", "canonical", "alternate", "preconnect", ""} {
		doc := rewriteDoc(t, `<link rel="`+rel+`" href="/r.css">`, examplePage)
		if got := attr(t, doc, "link", "href"); got != "http://example.com/r.css" {
			t.Errorf("link rel=%q href = %q, want http://example.com/r.css", rel, got)
		}
	}
}

// TestRewriteNonHTTPSchemesVerbatim covers "non-http(s) and empty/fragment refs
// are left verbatim".
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

// TestRewriteSrcset covers FR5 for srcset: each candidate URL becomes absolute
// and descriptors are kept, following the HTML srcset grammar (commas inside a
// URL, trailing commas, data: URLs, link imagesrcset).
func TestRewriteSrcset(t *testing.T) {
	cases := []struct {
		name string
		html string
		sel  string
		attr string
		want string
	}{
		{
			"relative candidates and descriptors",
			`<img srcset="/a.png 1x, /b.png 2x, c.png 640w">`, "img", "srcset",
			"http://example.com/a.png 1x, http://example.com/b.png 2x, http://example.com/dir/c.png 640w",
		},
		{
			"protocol-relative and ../ candidates",
			`<img srcset="//cdn.example.com/a.png 1x,../b.png 2x">`, "img", "srcset",
			"http://cdn.example.com/a.png 1x, http://example.com/b.png 2x",
		},
		{
			"comma inside a CDN URL",
			`<img srcset="https://cdn.example.com/w_100,h_100/a.jpg 1x, /w_200,h_200/a.jpg 2x">`, "img", "srcset",
			"https://cdn.example.com/w_100,h_100/a.jpg 1x, http://example.com/w_200,h_200/a.jpg 2x",
		},
		{
			"data URL kept, http candidate rewritten",
			`<img srcset="data:image/png;base64,AA,BB 1x, /b.png 2x">`, "img", "srcset",
			"data:image/png;base64,AA,BB 1x, http://example.com/b.png 2x",
		},
		{
			"non-http candidate kept",
			`<img srcset="about:blank 1x, /b.png 2x">`, "img", "srcset",
			"about:blank 1x, http://example.com/b.png 2x",
		},
		{
			"candidate without descriptor and trailing commas",
			`<img srcset="a.png,, b.png 2x,">`, "img", "srcset",
			"http://example.com/dir/a.png, http://example.com/dir/b.png 2x",
		},
		{
			"extra whitespace",
			"<img srcset=\"  a.png \n 1x ,\tb.png   2x  \">", "img", "srcset",
			"http://example.com/dir/a.png 1x, http://example.com/dir/b.png 2x",
		},
		{
			"source srcset",
			`<picture><source srcset="/s.png 1x"></picture>`, "source", "srcset",
			"http://example.com/s.png 1x",
		},
		{
			"link imagesrcset",
			`<link rel="preload" as="image" imagesrcset="/p.png 1x, /p2.png 2x">`, "link", "imagesrcset",
			"http://example.com/p.png 1x, http://example.com/p2.png 2x",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := rewriteDoc(t, c.html, examplePage)
			if got := attr(t, doc, c.sel, c.attr); got != c.want {
				t.Errorf("%s[%s] = %q, want %q", c.sel, c.attr, got, c.want)
			}
		})
	}
}

// --- Base href ----------------------------------------------------------------

// TestBaseInjectedWhenAbsent covers "a <base href> is injected as the first
// head child when none exists".
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

// TestBaseFixedWhenPresent covers "an existing relative <base href> is resolved
// to absolute against the page URL".
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

// TestBaseMovedToHeadStart covers FR5: an existing <base> anywhere in the
// document is moved to the start of <head>, keeping its other attributes, so
// the injected meta and shim can follow it.
func TestBaseMovedToHeadStart(t *testing.T) {
	for name, in := range map[string]string{
		"after other head elements": `<html><head><title>t</title><script>var a=1</script><base href="/app/" target="_self"></head></html>`,
		"in body":                   `<html><head><title>t</title></head><body><p>x</p><base href="/app/" target="_self"></body></html>`,
	} {
		t.Run(name, func(t *testing.T) {
			doc := rewriteDoc(t, in, examplePage)
			if n := doc.Find("base").Length(); n != 1 {
				t.Fatalf("base count = %d, want 1", n)
			}
			first := doc.Find("head").Children().First()
			if goquery.NodeName(first) != "base" {
				t.Fatalf("first head child = %q, want base", goquery.NodeName(first))
			}
			if href, _ := first.Attr("href"); href != "http://example.com/app/" {
				t.Errorf("base href = %q, want http://example.com/app/", href)
			}
			if target, _ := first.Attr("target"); target != "_self" {
				t.Errorf("base target = %q, want _self kept", target)
			}
		})
	}
}

// TestBaseNonHTTPFallsBackToPage covers FR5: a <base href> that resolves to a
// non-http(s) scheme (which browsers ignore) is reset to the page URL, so
// relative refs still resolve against the upstream.
func TestBaseNonHTTPFallsBackToPage(t *testing.T) {
	doc := rewriteDoc(t, `<html><head><base href="javascript:alert(1)"></head><body><img src="a.png"></body></html>`, examplePage)
	if href := attr(t, doc, "base", "href"); href != examplePage {
		t.Errorf("base href = %q, want the page URL %q", href, examplePage)
	}
	if got := attr(t, doc, "img", "src"); got != "http://example.com/dir/a.png" {
		t.Errorf("img src = %q, want http://example.com/dir/a.png", got)
	}
}

// TestBaseIgnoresTemplateAndForeignContent covers RV2: a <base href> inside
// <template> contents or in SVG/MathML content does not set the document base
// URL in a browser, so the rewriter ignores it. It stays where it is, the base
// the rewriter uses is the page URL (or the first HTML-namespace base), and
// relative refs resolve against that.
func TestBaseIgnoresTemplateAndForeignContent(t *testing.T) {
	cases := map[string]string{
		"template in head": `<html><head><template><base href="http://evil.example/t/"></template></head><body><img src="a.png"></body></html>`,
		"template in body": `<html><head></head><body><template><div><base href="http://evil.example/t/"></div></template><img src="a.png"></body></html>`,
		"svg":              `<html><head></head><body><svg><base href="http://evil.example/s/"></base></svg><img src="a.png"></body></html>`,
		"math":             `<html><head></head><body><math><base href="http://evil.example/m/"></base></math><img src="a.png"></body></html>`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			doc := rewriteDoc(t, in, examplePage)
			first := doc.Find("head").Children().First()
			if goquery.NodeName(first) != "base" {
				t.Fatalf("first head child = %q, want the injected base", goquery.NodeName(first))
			}
			if href, _ := first.Attr("href"); href != examplePage {
				t.Errorf("document base href = %q, want the page URL %q", href, examplePage)
			}
			if got := attr(t, doc, "img", "src"); got != "http://example.com/dir/a.png" {
				t.Errorf("img src = %q, want it resolved against the page URL", got)
			}
			if n := doc.Find(`base[href^="http://evil.example/"]`).Length(); n != 1 {
				t.Errorf("ignored base count = %d, want it left in place", n)
			}
		})
	}

	// A real HTML base after an ignored one is still used.
	doc := rewriteDoc(t, `<html><head><template><base href="http://evil.example/t/"></template><base href="/app/"></head><body><img src="a.png"></body></html>`, examplePage)
	if href := attr(t, doc, "head > base", "href"); href != "http://example.com/app/" {
		t.Errorf("document base href = %q, want http://example.com/app/", href)
	}
	if got := attr(t, doc, "img", "src"); got != "http://example.com/app/a.png" {
		t.Errorf("img src = %q, want http://example.com/app/a.png", got)
	}
}

// TestBaseAffectsRelativeResolution covers "refs resolve against the effective
// base (page URL combined with existing <base>)".
func TestBaseAffectsRelativeResolution(t *testing.T) {
	doc := rewriteDoc(t, `<html><head><base href="http://other.com/x/"></head><body><img src="a.png"></body></html>`, examplePage)
	if got := attr(t, doc, "img", "src"); got != "http://other.com/x/a.png" {
		t.Errorf("img src = %q, want http://other.com/x/a.png", got)
	}
}

// --- FR5: referrer meta and sandbox compatibility shim -------------------------

// TestInjectReferrerMetaAndShim covers FR5: <meta name="referrer"
// content="no-referrer"> and the verbatim sandbox shim are injected exactly
// once, directly after <base> at the start of <head>, for documents with and
// without their own <head>/<base>.
func TestInjectReferrerMetaAndShim(t *testing.T) {
	cases := map[string]string{
		"full document":          `<!doctype html><html><head><meta charset="utf-8"><title>t</title><script>var x=1</script></head><body>b</body></html>`,
		"existing base":          `<html><head><title>t</title><base href="/app/"></head><body>b</body></html>`,
		"fragment without head":  `<p>just a fragment</p><script>var y=2</script>`,
		"head-only scripts":      `<html><head><script src="/first.js"></script></head></html>`,
		"upstream referrer meta": `<html><head><meta name="referrer" content="unsafe-url"><meta name="Referrer" content="origin"></head><body><meta name="REFERRER" content="unsafe-url"></body></html>`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			doc := rewriteDoc(t, in, examplePage)
			kids := doc.Find("head").Children()
			if kids.Length() < 3 {
				t.Fatalf("head has %d children, want at least base, meta, script", kids.Length())
			}
			if n := goquery.NodeName(kids.Eq(0)); n != "base" {
				t.Fatalf("head child 0 = %q, want base", n)
			}
			meta := kids.Eq(1)
			if n := goquery.NodeName(meta); n != "meta" {
				t.Fatalf("head child 1 = %q, want meta", n)
			}
			if v, _ := meta.Attr("name"); v != "referrer" {
				t.Errorf("meta name = %q, want referrer", v)
			}
			if v, _ := meta.Attr("content"); v != "no-referrer" {
				t.Errorf("meta content = %q, want no-referrer", v)
			}
			if !isShim(kids.Eq(2)) {
				t.Fatalf("head child 2 is not the shim: %q", goquery.NodeName(kids.Eq(2)))
			}

			// Exactly once.
			referrerMetas := doc.Find("meta[name]").FilterFunction(func(_ int, s *goquery.Selection) bool {
				v, _ := s.Attr("name")
				return strings.EqualFold(v, "referrer")
			})
			if referrerMetas.Length() != 1 {
				t.Errorf("referrer metas = %d, want 1 (upstream ones removed)", referrerMetas.Length())
			}
			shims := doc.Find("script").FilterFunction(func(_ int, s *goquery.Selection) bool { return isShim(s) })
			if shims.Length() != 1 {
				t.Errorf("shim scripts = %d, want 1", shims.Length())
			}
		})
	}
}

// TestInjectShimVerbatim covers FR5: the shim text is emitted unescaped and
// byte-for-byte, so it runs exactly as tested in R1.
func TestInjectShimVerbatim(t *testing.T) {
	out, err := rewriteHTML([]byte(`<html><head></head><body></body></html>`), mustParseURL(t, examplePage), "text/html", nil)
	if err != nil {
		t.Fatalf("rewriteHTML: %v", err)
	}
	want := `<base href="` + examplePage + `"/><meta name="referrer" content="no-referrer"/>` + injectedShimMarkup
	if !strings.Contains(string(out), want) {
		t.Fatalf("output does not contain %q directly in order:\n%s", want, out)
	}
	if strings.Count(string(out), sandboxCompatShim) != 1 {
		t.Fatalf("shim text appears %d times, want 1", strings.Count(string(out), sandboxCompatShim))
	}
}

// TestInjectIdempotent covers FR5: rewriting an already rewritten page (an
// upstream that is itself a proxied page) still yields exactly one base, one
// referrer meta and one shim.
func TestInjectIdempotent(t *testing.T) {
	page := mustParseURL(t, examplePage)
	once, err := rewriteHTML([]byte(`<html><head><title>t</title></head><body><img src="a.png"></body></html>`), page, "text/html", nil)
	if err != nil {
		t.Fatalf("first rewrite: %v", err)
	}
	twice, err := rewriteHTML(once, page, "text/html", nil)
	if err != nil {
		t.Fatalf("second rewrite: %v", err)
	}
	if !bytes.Equal(once, twice) {
		t.Fatalf("rewrite is not idempotent:\n once=%s\ntwice=%s", once, twice)
	}
}

// TestRemoveReferrerPolicyAttributes covers FR5: element referrerpolicy
// attributes, which would override the injected no-referrer policy for that
// request, are removed.
func TestRemoveReferrerPolicyAttributes(t *testing.T) {
	doc := rewriteDoc(t, `<html><body><img src="a.png" referrerpolicy="unsafe-url"><a href="/x" referrerpolicy="origin">x</a><iframe src="/f" referrerpolicy="unsafe-url"></iframe></body></html>`, examplePage)
	if n := doc.Find("[referrerpolicy]").Length(); n != 0 {
		t.Fatalf("%d elements still carry referrerpolicy", n)
	}
}

// --- Meta removal ---------------------------------------------------------------

// TestRemoveCSPAndRefreshMetas covers "CSP and refresh meta tags removed;
// charset/viewport/XFO-meta preserved".
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

// --- Frame-buster removal ---------------------------------------------------------

// TestRemoveFrameBusterFull covers "an inline frame-buster with BOTH a
// comparison and a navigation marker is removed".
func TestRemoveFrameBusterFull(t *testing.T) {
	cases := []string{
		`if (top != self) { top.location = self.location; }`,
		`if (window.top !== window.self) window.top.location.replace(location.href);`,
		`if (parent.frames.length > 0) parent.location.href = document.location.href;`,
		`if(window.frameElement){ top.location.href = window.location.href; }`,
	}
	for _, js := range cases {
		doc := rewriteDoc(t, `<html><body><script>`+js+`</script></body></html>`, examplePage)
		if pageScripts(doc).Length() != 0 {
			t.Errorf("frame-buster not removed: %q", js)
		}
	}
}

// TestRemoveFrameBusterSingleMarkerKept covers "a script with only ONE marker
// (comparison OR navigation, not both) is KEPT (false-negative bias)".
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
			if pageScripts(doc).Length() != 1 {
				t.Errorf("script should be KEPT (only partial markers): %q", c.js)
			}
		})
	}
}

// TestFrameBusterExternalScriptKept covers "external scripts (with src) are
// never scanned/removed; their body is not fetched".
func TestFrameBusterExternalScriptKept(t *testing.T) {
	doc := rewriteDoc(t, `<html><body><script src="/buster.js"></script></body></html>`, examplePage)
	if pageScripts(doc).Length() != 1 {
		t.Fatal("external script should be kept")
	}
	// Its src is the absolute upstream URL (FR5).
	if got := attr(t, doc, "script[src]", "src"); got != "http://example.com/buster.js" {
		t.Errorf("external script src = %q, want http://example.com/buster.js", got)
	}
}

// TestFrameBusterLdJSONUntouched covers "a non-executable script type
// (application/ld+json) is never removed even if it contains marker substrings".
func TestFrameBusterLdJSONUntouched(t *testing.T) {
	js := `{"note":"if (top != self) top.location = self.location"}`
	doc := rewriteDoc(t, `<html><body><script type="application/ld+json">`+js+`</script></body></html>`, examplePage)
	if pageScripts(doc).Length() != 1 {
		t.Error("ld+json data block must not be removed")
	}
}

// TestFrameBusterModuleAndTextJavascriptScanned confirms type="module" and
// type="text/javascript" inline scripts ARE scanned.
func TestFrameBusterModuleAndTextJavascriptScanned(t *testing.T) {
	for _, typ := range []string{"module", "text/javascript", "application/javascript"} {
		js := `if (top != self) { top.location = self.location; }`
		doc := rewriteDoc(t, `<html><body><script type="`+typ+`">`+js+`</script></body></html>`, examplePage)
		if pageScripts(doc).Length() != 0 {
			t.Errorf("inline frame-buster with type=%q should be removed", typ)
		}
	}
}

// --- Security / XSS ---------------------------------------------------------------

// TestRewriteEscapesHostileURL covers "a crafted upstream URL containing
// \"><script> ends up escaped inside the attribute (no breakout)".
func TestRewriteEscapesHostileURL(t *testing.T) {
	hostile := `/p"><script>alert(1)</script>`
	out, err := rewriteHTML([]byte(`<a href='`+hostile+`'>x</a>`), mustParseURL(t, examplePage), "text/html", nil)
	if err != nil {
		t.Fatalf("rewriteHTML: %v", err)
	}
	s := withoutInjectedShim(string(out))
	// The raw breakout sequence must NOT appear literally in the output.
	if strings.Contains(s, `"><script>alert(1)</script>`) || strings.Contains(s, "<script>") {
		t.Fatalf("hostile URL broke out of the attribute: %s", out)
	}
	// Re-parsing must yield exactly one <a> and no upstream <script> elements.
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if pageScripts(doc).Length() != 0 {
		t.Fatalf("an injected <script> survived; output: %s", out)
	}
	if href := attr(t, doc, "a", "href"); !strings.HasPrefix(href, "http://example.com/p") {
		t.Fatalf("a href = %q, want the absolute, escaped upstream URL", href)
	}
}

// --- Charset ----------------------------------------------------------------------

// TestRewriteCharsetAware covers "non-UTF-8 input is decoded via
// x/net/html/charset and emitted as UTF-8".
func TestRewriteCharsetAware(t *testing.T) {
	// "café" in ISO-8859-1: é is 0xE9.
	body := []byte("<html><head><meta charset=\"ISO-8859-1\"></head><body>caf\xe9</body></html>")
	out, err := rewriteHTML(body, mustParseURL(t, examplePage), "text/html; charset=ISO-8859-1", nil)
	if err != nil {
		t.Fatalf("rewriteHTML: %v", err)
	}
	if !bytes.Contains(out, []byte("café")) {
		t.Errorf("expected UTF-8 'café' in output, got %q", string(out))
	}
}

// --- Malformed HTML ---------------------------------------------------------------

// TestRewriteMalformedNoPanic covers "malformed HTML does not panic; rewriting
// still produces output".
func TestRewriteMalformedNoPanic(t *testing.T) {
	cases := []string{
		`<html><body><img src="/a.png"`,       // unclosed tag
		`<a href="/x"><b><i>unbalanced</a>`,   // mis-nested
		`plain text no tags`,                  // no markup
		`<<<>>><img src=/a.png>`,              // garbage + bare attr
		`<!doctype html><HtMl><BoDy>x</BoDy>`, // mixed case, missing close
	}
	for _, in := range cases {
		out, err := rewriteHTML([]byte(in), mustParseURL(t, examplePage), "text/html", nil)
		if err != nil {
			t.Errorf("rewriteHTML(%q) errored: %v", in, err)
		}
		if len(out) == 0 {
			t.Errorf("rewriteHTML(%q) produced empty output", in)
		}
	}
	// Empty input is returned unchanged (no parse, no error).
	if out, err := rewriteHTML([]byte(``), mustParseURL(t, examplePage), "text/html", nil); err != nil || len(out) != 0 {
		t.Errorf("rewriteHTML(empty) = (%q, %v), want ([], nil)", string(out), err)
	}
}

// --- Integration through ModifyResponse / prepareHTMLBody ---------------------------

// TestRewriteThroughProxyIntegration covers FR5 through the full /proxy path:
// absolute upstream URLs, the base, the referrer meta and the shim.
func TestRewriteThroughProxyIntegration(t *testing.T) {
	const html = `<html><head><title>t</title></head><body><img src="/logo.png" srcset="/logo2.png 2x"><a href="next">go</a></body></html>`
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
	body := rec.Body.String()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse proxied body: %v", err)
	}
	if got := attr(t, doc, "img", "src"); got != "http://example.com/logo.png" {
		t.Errorf("img src = %q, want http://example.com/logo.png", got)
	}
	if got := attr(t, doc, "img", "srcset"); got != "http://example.com/logo2.png 2x" {
		t.Errorf("img srcset = %q, want http://example.com/logo2.png 2x", got)
	}
	if got := attr(t, doc, "a", "href"); got != "http://example.com/dir/next" {
		t.Errorf("a href = %q, want http://example.com/dir/next", got)
	}
	if strings.Contains(body, "/api/plugins/") {
		t.Errorf("rewritten body still references the Grafana plugin API: %s", body)
	}
	if doc.Find("head base").Length() != 1 || doc.Find(`meta[name="referrer"]`).Length() != 1 {
		t.Error("base or referrer meta missing through proxy")
	}
	if strings.Count(body, sandboxCompatShim) != 1 {
		t.Error("shim missing or duplicated through proxy")
	}
}

// TestRewriteContentLengthUpdated covers "Content-Length is updated to the
// rewritten length through ModifyResponse".
func TestRewriteContentLengthUpdated(t *testing.T) {
	const html = `<html><body><img src="/a.png"></body></html>`
	resp := &http.Response{Header: http.Header{}, Body: nopBody(html)}
	resp.Header.Set("Content-Type", "text/html")
	if err := prepareHTMLBody(resp, 1<<20, mustParseURL(t, examplePage), nil, nil); err != nil {
		t.Fatalf("prepareHTMLBody: %v", err)
	}
	body := readAllString(t, resp)
	if resp.Header.Get("Content-Length") != itoa(len(body)) {
		t.Errorf("Content-Length header = %q, want %d", resp.Header.Get("Content-Length"), len(body))
	}
	if resp.ContentLength != int64(len(body)) {
		t.Errorf("resp.ContentLength = %d, want %d", resp.ContentLength, len(body))
	}
	if !strings.Contains(body, "http://example.com/a.png") {
		t.Errorf("body not rewritten: %q", body)
	}
}

// TestNonHTMLByteIdenticalPassthrough covers "non-HTML responses pass through
// byte-identical (regression)". A non-HTML body must NOT be rewritten even if
// it looks like HTML.
func TestNonHTMLByteIdenticalPassthrough(t *testing.T) {
	const payload = `<html><body><img src="/a.png"></body></html>` // looks like HTML, but JSON content-type
	resp := &http.Response{Header: http.Header{}, Body: nopBody(payload)}
	resp.Header.Set("Content-Type", "application/json")
	resp.ContentLength = int64(len(payload))
	if err := prepareHTMLBody(resp, 1<<20, mustParseURL(t, examplePage), nil, nil); err != nil {
		t.Fatalf("prepareHTMLBody: %v", err)
	}
	if body := readAllString(t, resp); body != payload {
		t.Errorf("non-HTML body modified: got %q, want byte-identical %q", body, payload)
	}
}

// TestRewriteFailureFailsClosed covers RV2: a rewriter failure other than the
// size limit fails closed. prepareHTMLBody returns an error that wraps
// errRewriteFailed (so the ReverseProxy sends 502), and the un-rewritten
// original is never placed on the response.
func TestRewriteFailureFailsClosed(t *testing.T) {
	orig := htmlRewriter
	t.Cleanup(func() { htmlRewriter = orig })
	htmlRewriter = func(_ []byte, _ *url.URL, _ string, _ []string, _ int64) ([]byte, error) {
		return nil, errInjectedRewriteFailure
	}

	const html = `<html><body><img src="/a.png">original</body></html>`
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: nopBody(html)}
	resp.Header.Set("Content-Type", "text/html")

	err := prepareHTMLBody(resp, 1<<20, mustParseURL(t, examplePage), nil, nil)
	if !errors.Is(err, errRewriteFailed) || !errors.Is(err, errInjectedRewriteFailure) {
		t.Fatalf("prepareHTMLBody error = %v, want errRewriteFailed wrapping the rewriter error", err)
	}
	rec := httptest.NewRecorder()
	if reason := proxyErrorHandler(rec, nil, err); reason != denialReasonUpstream || rec.Code != http.StatusBadGateway {
		t.Fatalf("proxyErrorHandler = %q/%d, want %q/502", reason, rec.Code, denialReasonUpstream)
	}
	if strings.Contains(rec.Body.String(), "original") {
		t.Errorf("error body leaks the original page: %q", rec.Body.String())
	}
}

// TestProxyParseFailureFailsClosed covers RV2 through /proxy: a document nested
// deeper than the HTML parser accepts (more than 512 open elements) is a 502
// with none of the upstream markup, not the un-rewritten original.
func TestProxyParseFailureFailsClosed(t *testing.T) {
	page := "<html><head></head><body>" + strings.Repeat("<div>", 600) + "secret-marker" + strings.Repeat("</div>", 600) + "</body></html>"
	for _, gz := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "gzip"}[gz], func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				body := []byte(page)
				if gz {
					w.Header().Set("Content-Encoding", "gzip")
					body = gzipBytes(t, body)
				}
				_, _ = w.Write(body)
			}))
			defer upstream.Close()
			p := newTestHandler(t, settingsWith(allowExample(DomainOptions{})), upstream)

			rec := doProxy(p, "http://example.com/deep")
			if rec.Code != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502", rec.Code)
			}
			if strings.Contains(rec.Body.String(), "secret-marker") || strings.Contains(rec.Body.String(), "<div>") {
				t.Errorf("502 body leaks upstream markup: %.200q", rec.Body.String())
			}
		})
	}
}

// errInjectedRewriteFailure is the deterministic error a test rewriter returns to
// exercise the fail-closed path.
var errInjectedRewriteFailure = errorString("injected rewrite failure")

type errorString string

func (e errorString) Error() string { return string(e) }
