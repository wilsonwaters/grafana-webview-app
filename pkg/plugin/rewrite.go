package plugin

import (
	"bytes"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

// resourceBase is the base path under which this plugin's backend resources are
// registered by Grafana. Rewritten URLs are emitted as ORIGIN-ABSOLUTE paths
// under this base so the browser resolves them against the Grafana origin (and
// the injected <base href> therefore cannot corrupt them).
const resourceBase = "/api/plugins/wilsonwaters-webview-app/resources"

// proxyResourcePath is the resource path the subresource-proxy endpoint will be
// registered under (CR3). CR2 only EMITS URLs pointing at it; until CR3 lands
// these requests 404, which is acceptable per the task scope.
const proxyResourcePath = "/proxy-resource"

// proxyResourceURL builds an origin-absolute subresource-proxy URL for an
// already-resolved absolute upstream URL: it query-encodes target (Q9 decision)
// into /api/.../proxy-resource?url=<enc>. The URL is built entirely through the
// Go URL API (url.Values.Encode) — never string concatenation — so the upstream
// URL is percent-encoded and cannot inject into the query string. A trailing
// fragment is stripped before encoding (the fragment is not part of the resource
// fetch; the browser does not send it upstream anyway).
func proxyResourceURL(target string) string {
	return buildProxyURL(resourceBase+proxyResourcePath, target)
}

// proxyNavURL builds an origin-absolute top-level-proxy URL for an
// already-resolved absolute upstream URL. Navigation refs (a/area/iframe, GET
// forms) route back through /proxy so the destination page is itself rewritten.
// Same query-encoding contract as proxyResourceURL.
func proxyNavURL(target string) string {
	return buildProxyURL(resourceBase+proxyPath, target)
}

// buildProxyURL assembles basePath?url=<enc(target)> via url.Values so target is
// percent-encoded. Any fragment on target is dropped before encoding.
func buildProxyURL(basePath, target string) string {
	enc := target
	if i := strings.IndexByte(enc, '#'); i >= 0 {
		enc = enc[:i]
	}
	q := url.Values{}
	q.Set("url", enc)
	return basePath + "?" + q.Encode()
}

// buildRedirectProxyURL assembles basePath?url=<enc(target)>&_wvredir=<depth> via
// url.Values (CR4). It is buildProxyURL plus the reserved redirect-hop depth
// control param: when the browser follows a rewritten Location, serve reads
// _wvredir back to learn the hop depth and enforce the MaxRedirects cap. Both
// params are url.Values-encoded so the upstream target cannot inject into the
// query string, and a fragment on target is dropped (as in buildProxyURL).
func buildRedirectProxyURL(basePath, target string, depth int) string {
	enc := target
	if i := strings.IndexByte(enc, '#'); i >= 0 {
		enc = enc[:i]
	}
	q := url.Values{}
	q.Set("url", enc)
	q.Set(wvRedirParam, strconv.Itoa(depth))
	return basePath + "?" + q.Encode()
}

// subresourceAttr describes one (selector, attribute) pair whose URL value is a
// SUBRESOURCE the browser loads automatically (image, script, stylesheet, …).
// These are rewritten to /proxy-resource so the fetch re-enters the security
// pipeline at CR3.
type subresourceAttr struct {
	selector string
	attr     string
}

// navAttr describes one (selector, attribute) pair whose URL value is a
// NAVIGATION target (a link, an embedded document, a GET form action). These are
// rewritten to /proxy so the destination is itself proxied + rewritten.
type navAttr struct {
	selector string
	attr     string
}

// subresourceAttrs is the exact subresource attribute set per the approved CR2
// design. link[href] is handled separately (rel-gated) in rewriteLinks.
var subresourceAttrs = []subresourceAttr{
	{"img", "src"},
	{"script", "src"},
	{"source", "src"},
	{"video", "src"},
	{"audio", "src"},
	{"track", "src"},
	{"object", "data"},
	{"embed", "src"},
}

// subresourceSrcsetAttrs is the set of (selector, srcset) pairs rewritten by
// rewriteSrcset (comma-separated candidate lists).
var subresourceSrcsetAttrs = []subresourceAttr{
	{"img", "srcset"},
	{"source", "srcset"},
}

// navAttrs is the exact navigation attribute set per the approved CR2 design.
// form[action] is handled separately (method-gated) in rewriteForms — only GET /
// absent-method forms are rewritten; POST forms are left verbatim.
var navAttrs = []navAttr{
	{"a", "href"},
	{"area", "href"},
	{"iframe", "src"},
}

// linkRewritableRels is the set of <link rel> tokens whose href is treated as a
// rewritable SUBRESOURCE. A <link> with no rel is also rewritten (rel absent).
// rel values not in this set (e.g. canonical, alternate, dns-prefetch,
// preconnect) are left verbatim — they are not subresources the proxy serves.
var linkRewritableRels = map[string]bool{
	"stylesheet":       true,
	"icon":             true,
	"shortcut icon":    true,
	"apple-touch-icon": true,
	"mask-icon":        true,
	"preload":          true,
	"modulepreload":    true,
	"manifest":         true,
}

// htmlRewriter is the rewrite function prepareHTMLBody invokes on HTML bodies. It
// defaults to rewriteHTML and is a package var ONLY so a test can substitute a
// deterministically-failing rewriter to exercise the degradation path (serve the
// decoded original on rewrite error). Production never reassigns it.
var htmlRewriter = rewriteHTML

// rewriteHTML parses html (charset-aware, per contentType + a <meta charset>),
// applies the CR2 rewrites (base href, subresource/navigation URL rewriting,
// srcset, CSP/refresh meta removal, frame-buster removal), and returns the
// re-rendered document as UTF-8. All DOM mutation goes through the goquery API
// and the goquery renderer, which HTML-escapes attribute values — there is no
// string concatenation of HTML, so a hostile upstream URL cannot break out of an
// attribute. On any parse error it returns the error so the caller can degrade
// to serving the decoded original (the security gates already ran).
func rewriteHTML(htmlBytes []byte, pageURL *url.URL, contentType string) ([]byte, error) {
	// An empty body has nothing to rewrite (and charset.NewReader would surface an
	// EOF on it); return it unchanged rather than treating it as a parse failure.
	if len(htmlBytes) == 0 {
		return htmlBytes, nil
	}
	// Charset-aware decode: charset.NewReader sniffs the BOM / <meta charset> /
	// the supplied Content-Type and yields a UTF-8 reader, so goquery (which
	// assumes UTF-8) parses correctly regardless of the upstream encoding.
	reader, err := charset.NewReader(bytes.NewReader(htmlBytes), contentType)
	if err != nil {
		return nil, fmt.Errorf("charset reader: %w", err)
	}
	doc, err := goquery.NewDocumentFromReader(reader)
	if err != nil {
		return nil, fmt.Errorf("parse html: %w", err)
	}

	base := injectOrFixBase(doc, pageURL)

	rewriteRefs(doc, base)
	rewriteSrcset(doc, base)
	rewriteLinks(doc, base)
	rewriteNav(doc, base)
	rewriteForms(doc, base)
	removeCSPMetas(doc)
	removeFrameBusters(doc)

	out, err := goquery.OuterHtml(doc.Selection)
	if err != nil {
		return nil, fmt.Errorf("render html: %w", err)
	}
	return []byte(out), nil
}

// injectOrFixBase computes the effective base for relative-URL resolution and
// ensures the document carries a <base href> pointing at the absolute effective
// base. If a <base href> already exists it is RESOLVED against pageURL and set to
// the absolute result (so a relative existing base still produces an absolute
// upstream base); otherwise a <base href="<pageURL>"> is injected as the first
// child of <head>. It returns the effective base *url.URL used to resolve every
// other ref in the document. The injected base is a backstop for refs CR2 does
// not rewrite (runtime-JS URLs, CSS url(), out-of-set attributes); rewritten refs
// are origin-absolute and unaffected by it.
func injectOrFixBase(doc *goquery.Document, pageURL *url.URL) *url.URL {
	base := pageURL
	existing := doc.Find("base[href]").First()
	if existing.Length() > 0 {
		if href, ok := existing.Attr("href"); ok {
			if ref, err := url.Parse(strings.TrimSpace(href)); err == nil {
				base = pageURL.ResolveReference(ref)
			}
		}
		existing.SetAttr("href", base.String())
		return base
	}

	// No <base>: inject one as the first child of <head>. If there is no <head>
	// (malformed doc), fall back to prepending to <html>, then to the document.
	node := &html.Node{
		Type: html.ElementNode,
		Data: "base",
		Attr: []html.Attribute{{Key: "href", Val: base.String()}},
	}
	baseSel := newNodeSelection(doc, node)
	if head := doc.Find("head").First(); head.Length() > 0 {
		head.PrependSelection(baseSel)
	} else if htmlEl := doc.Find("html").First(); htmlEl.Length() > 0 {
		htmlEl.PrependSelection(baseSel)
	} else {
		doc.PrependSelection(baseSel)
	}
	return base
}

// newNodeSelection wraps a freshly-created html.Node in a goquery Selection so it
// can be inserted via the goquery API (PrependSelection etc.).
func newNodeSelection(doc *goquery.Document, node *html.Node) *goquery.Selection {
	s := doc.Find("__never_matches_anything__")
	return s.AddNodes(node)
}

// rewriteRefs rewrites every plain subresource attribute (img[src], script[src],
// …) to a /proxy-resource URL when the ref resolves to an http/https target.
func rewriteRefs(doc *goquery.Document, base *url.URL) {
	for _, sa := range subresourceAttrs {
		doc.Find(sa.selector).Each(func(_ int, s *goquery.Selection) {
			rewriteAttr(s, sa.attr, base, false)
		})
	}
}

// rewriteLinks rewrites <link href> only when its rel is rewritable (in
// linkRewritableRels) or absent; other rels (canonical, alternate, …) are left
// verbatim. Matching is case-insensitive and whitespace-normalised.
func rewriteLinks(doc *goquery.Document, base *url.URL) {
	doc.Find("link[href]").Each(func(_ int, s *goquery.Selection) {
		rel, hasRel := s.Attr("rel")
		if hasRel {
			rel = strings.ToLower(strings.Join(strings.Fields(rel), " "))
			if rel != "" && !linkRewritableRels[rel] {
				return
			}
		}
		rewriteAttr(s, "href", base, false)
	})
}

// rewriteForms rewrites <form action> only for GET forms (method absent or
// "get", case-insensitive). POST forms are left verbatim — proxying form POSTs is
// out of scope. A GET form's action is a NAVIGATION ref.
func rewriteForms(doc *goquery.Document, base *url.URL) {
	doc.Find("form[action]").Each(func(_ int, s *goquery.Selection) {
		if method, ok := s.Attr("method"); ok && !strings.EqualFold(strings.TrimSpace(method), "get") {
			return
		}
		rewriteAttr(s, "action", base, true)
	})
}

// rewriteNav rewrites the navigation attribute set (a/area/iframe) to /proxy URLs.
func rewriteNav(doc *goquery.Document, base *url.URL) {
	for _, na := range navAttrs {
		doc.Find(na.selector).Each(func(_ int, s *goquery.Selection) {
			rewriteAttr(s, na.attr, base, true)
		})
	}
}

// rewriteAttr resolves the named attribute against base and, if it is a
// rewritable http/https ref, replaces it with a proxy URL (navigation → /proxy
// when nav is true, otherwise subresource → /proxy-resource). For navigation refs
// a trailing #fragment is preserved on the rewritten URL (outside the url= param)
// so in-page anchors and SPA-style fragment links keep working after navigation.
func rewriteAttr(s *goquery.Selection, attr string, base *url.URL, nav bool) {
	raw, ok := s.Attr(attr)
	if !ok {
		return
	}
	abs, rewritable := resolveAndClassify(raw, base)
	if !rewritable {
		return
	}
	if nav {
		rewritten := proxyNavURL(abs)
		if frag := fragmentOf(raw); frag != "" {
			rewritten += "#" + frag
		}
		s.SetAttr(attr, rewritten)
		return
	}
	s.SetAttr(attr, proxyResourceURL(abs))
}

// fragmentOf returns the fragment portion (after '#', un-decoded) of ref, or "".
func fragmentOf(ref string) string {
	if i := strings.IndexByte(ref, '#'); i >= 0 {
		return ref[i+1:]
	}
	return ""
}

// resolveAndClassify resolves ref against base and reports the absolute URL plus
// whether it is rewritable. A ref is rewritable ONLY when, after resolution, its
// scheme is http or https. Empty refs, pure fragments, and non-http(s) schemes
// (data:/blob:/mailto:/tel:/javascript:/about:/…) are NOT rewritable and are left
// verbatim by the caller. The returned absURL has its fragment stripped (the
// caller re-attaches it for navigation refs).
func resolveAndClassify(ref string, base *url.URL) (absURL string, rewritable bool) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", false
	}
	resolved := base.ResolveReference(parsed)
	switch strings.ToLower(resolved.Scheme) {
	case "http", "https":
	default:
		return "", false
	}
	resolved.Fragment = ""
	resolved.RawFragment = ""
	return resolved.String(), true
}

// rewriteSrcset rewrites srcset attributes (img/source) candidate-by-candidate.
// A srcset is a comma-separated list of candidates, each "url [descriptor]"
// (descriptor is an optional width "640w" or density "2x"). Each candidate URL is
// resolved + rewritten to /proxy-resource (subresource) when http/https; the
// descriptor is preserved verbatim. Non-rewritable URLs (data:, etc.) keep their
// original URL text. Commas inside the (already percent-encoded) rewritten URL
// cannot occur because url.Values.Encode escapes them, so re-joining with ", "
// is safe.
func rewriteSrcset(doc *goquery.Document, base *url.URL) {
	for _, sa := range subresourceSrcsetAttrs {
		doc.Find(sa.selector).Each(func(_ int, s *goquery.Selection) {
			raw, ok := s.Attr(sa.attr)
			if !ok || strings.TrimSpace(raw) == "" {
				return
			}
			s.SetAttr(sa.attr, rewriteSrcsetValue(raw, base))
		})
	}
}

// rewriteSrcsetValue rewrites a single srcset attribute value.
func rewriteSrcsetValue(srcset string, base *url.URL) string {
	candidates := strings.Split(srcset, ",")
	out := make([]string, 0, len(candidates))
	for _, cand := range candidates {
		trimmed := strings.TrimSpace(cand)
		if trimmed == "" {
			continue
		}
		// Split into URL + optional descriptor on the first run of whitespace.
		fields := strings.Fields(trimmed)
		urlPart := fields[0]
		descriptor := strings.TrimSpace(strings.TrimPrefix(trimmed, urlPart))
		if abs, rewritable := resolveAndClassify(urlPart, base); rewritable {
			urlPart = proxyResourceURL(abs)
		}
		if descriptor != "" {
			out = append(out, urlPart+" "+descriptor)
		} else {
			out = append(out, urlPart)
		}
	}
	return strings.Join(out, ", ")
}

// removeCSPMetas removes <meta http-equiv> elements whose http-equiv is a CSP
// directive (content-security-policy / -report-only) or "refresh". A CSP meta
// could re-impose framing/connection policy that would break the proxied embed;
// a refresh meta would navigate the panel out of the proxy. charset/viewport/etc.
// are left intact. An X-Frame-Options meta is inert (XFO is header-only, ignored
// in meta) and left in place.
func removeCSPMetas(doc *goquery.Document) {
	doc.Find("meta[http-equiv]").Each(func(_ int, s *goquery.Selection) {
		equiv, ok := s.Attr("http-equiv")
		if !ok {
			return
		}
		equiv = strings.TrimSpace(equiv)
		if strings.EqualFold(equiv, "content-security-policy") ||
			strings.EqualFold(equiv, "content-security-policy-report-only") ||
			strings.EqualFold(equiv, "refresh") {
			s.Remove()
		}
	})
}

// Q11 — Frame-buster removal (RESOLVED in OPEN-QUESTIONS Q11 / architecture-notes).
//
// Strategy: STATIC substring match on INLINE <script> text only. No JS is
// executed; external scripts (those with a src) are never scanned or removed.
// Header-level framing is already neutralised by stripFramingHeaders, so this is
// belt-and-braces with a deliberate FALSE-NEGATIVE bias: an inline script is
// removed ONLY when it contains BOTH a self-vs-top COMPARISON marker AND a
// navigation/escape NAVIGATION marker. Requiring the pair avoids nuking a
// legitimate script that merely mentions one phrase. Residual gaps (novel
// phrasings, busters in external JS) are accepted.
//
// Only scripts with NO src and a type that executes as classic/module JS are
// scanned: type absent, "text/javascript", "application/javascript", or "module".
// Data blocks like type="application/ld+json" are SKIPPED (never removed).
//
// Marker table (matched case-insensitively against the lower-cased script text):
//
//	COMPARISON markers (page detects it is framed):
//	  top != self        top !== self        self != top        self !== top
//	  top != window      window.top != window.self              window.top !== window.self
//	  window.self != window.top             window.self !== window.top
//	  window.frameelement (window.frameElement)
//	  parent.frames.length                  top == self / top === self (positive-guard form)
//
//	NAVIGATION markers (page breaks out by steering the top/parent frame):
//	  top.location =     top.location=      top.location.href  top.location.replace
//	  parent.location =  parent.location=   parent.location.href  parent.location.replace
//	  window.top.location                   self.parent.location
var frameBusterComparisonMarkers = []string{
	"top != self",
	"top !== self",
	"self != top",
	"self !== top",
	"top != window",
	"window.top != window.self",
	"window.top !== window.self",
	"window.self != window.top",
	"window.self !== window.top",
	"window.frameelement",
	"parent.frames.length",
	"top == self",
	"top === self",
}

var frameBusterNavigationMarkers = []string{
	"top.location =",
	"top.location=",
	"top.location.href",
	"top.location.replace",
	"parent.location =",
	"parent.location=",
	"parent.location.href",
	"parent.location.replace",
	"window.top.location",
	"self.parent.location",
}

// frameBusterScriptTypes are the <script> type values that execute as JS and are
// therefore scanned. An absent type is also scanned (it defaults to JS).
var frameBusterScriptTypes = map[string]bool{
	"text/javascript":        true,
	"application/javascript": true,
	"module":                 true,
}

// removeFrameBusters removes inline <script> elements that look like frame-busters
// per the Q11 marker-pair rule above.
func removeFrameBusters(doc *goquery.Document) {
	doc.Find("script").Each(func(_ int, s *goquery.Selection) {
		// External script: leave entirely (body is not fetched/scanned).
		if _, hasSrc := s.Attr("src"); hasSrc {
			return
		}
		// Only scan executable script types; skip data blocks (ld+json, etc.).
		if typ, ok := s.Attr("type"); ok {
			if !frameBusterScriptTypes[strings.ToLower(strings.TrimSpace(typ))] {
				return
			}
		}
		text := strings.ToLower(s.Text())
		if text == "" {
			return
		}
		if containsAny(text, frameBusterComparisonMarkers) && containsAny(text, frameBusterNavigationMarkers) {
			s.Remove()
		}
	})
}

// containsAny reports whether s contains any of the (already lower-cased) markers.
func containsAny(s string, markers []string) bool {
	for _, m := range markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}
