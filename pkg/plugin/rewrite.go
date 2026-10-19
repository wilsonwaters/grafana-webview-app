package plugin

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
	"github.com/wilsonwaters/webview/pkg/internal/htmlparse"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/net/html/charset"
)

// redirectProxyRef is the path of the redirect Location the proxy emits (CR4).
// It is relative ("proxy", the last segment of proxyPath), so the browser
// resolves it against the URL of the /proxy request it is following. The next
// hop therefore stays under the same .../resources/ path whatever sub-path
// Grafana is served from (root_url = https://host/grafana/).
const redirectProxyRef = "proxy"

// buildRedirectProxyURL returns the relative redirect Location
// proxy?url=<target>&_wvredir=<depth>[&hide=<selector>...] (CR4). When the
// browser follows it, ServeHTTP reads _wvredir back to enforce the MaxRedirects
// cap, and the hide selectors of the original request apply to the next hop
// too. url.Values encodes every parameter, so the upstream target cannot inject
// into the query string. A fragment on target is dropped.
func buildRedirectProxyURL(target string, depth int, hideSelectors []string) string {
	enc := target
	if i := strings.IndexByte(enc, '#'); i >= 0 {
		enc = enc[:i]
	}
	q := url.Values{}
	q.Set("url", enc)
	q.Set(wvRedirParam, strconv.Itoa(depth))
	if len(hideSelectors) > 0 {
		q[hideParam] = append([]string(nil), hideSelectors...)
	}
	return redirectProxyRef + "?" + q.Encode()
}

// urlAttr names an element attribute whose value is a URL. selector selects
// the elements that carry the attribute.
type urlAttr struct {
	selector string
	attr     string
}

// urlAttrs are the single-URL attributes the rewriter makes absolute (FR5).
// Subresources come first, then navigation targets. The panel renders the
// rewritten page in a sandboxed srcdoc iframe, so the browser loads
// subresources directly from the upstream and a followed link or nested frame
// navigates to the upstream URL; nothing routes back through Grafana.
var urlAttrs = []urlAttr{
	// Subresources the browser loads.
	{"img[src]", "src"},
	{"script[src]", "src"},
	{"link[href]", "href"},
	{"source[src]", "src"},
	{"video[src]", "src"},
	{"video[poster]", "poster"},
	{"audio[src]", "src"},
	{"track[src]", "src"},
	{"object[data]", "data"},
	{"embed[src]", "src"},
	{"input[src]", "src"},
	{"[background]", "background"}, // legacy body/table/td background image
	// Navigation targets.
	{"a[href]", "href"},
	{"area[href]", "href"},
	{"iframe[src]", "src"},
	{"frame[src]", "src"},
	{"form[action]", "action"},
	{"button[formaction]", "formaction"},
	{"input[formaction]", "formaction"},
}

// srcsetAttrs are the candidate-list attributes rewritten by rewriteSrcset.
var srcsetAttrs = []urlAttr{
	{"img[srcset]", "srcset"},
	{"source[srcset]", "srcset"},
	{"link[imagesrcset]", "imagesrcset"},
}

// referrerPolicyNoReferrer is the content of the injected referrer meta. It
// stops subresource and navigation requests from the framed page sending the
// Grafana dashboard URL to the upstream in the Referer header (R1 section 5).
const referrerPolicyNoReferrer = "no-referrer"

// sandboxCompatShim is the sandbox compatibility script from the R1 findings
// (section 10), injected verbatim. The panel renders the page in a
// sandbox="allow-scripts" srcdoc iframe, which has an opaque origin, so reading
// document.cookie, localStorage or sessionStorage throws a SecurityError in
// Chromium and Firefox and scripts that use them stop (the BOM radar loop, for
// example). The shim installs in-memory replacements, and only when the real
// API throws. It gives the page no access to Grafana.
const sandboxCompatShim = `(function(){var jar="";try{document.cookie}catch(e){try{Object.defineProperty(Document.prototype,"cookie",{configurable:true,get:function(){return jar},set:function(v){var kv=String(v).split(";")[0];var k=kv.split("=")[0];var parts=jar?jar.split("; ").filter(function(p){return p.split("=")[0]!==k}):[];parts.push(kv);jar=parts.join("; ")}})}catch(e2){}}function mem(){var s={};return{getItem:function(k){return Object.prototype.hasOwnProperty.call(s,k)?s[k]:null},setItem:function(k,v){s[k]=String(v)},removeItem:function(k){delete s[k]},clear:function(){s={}},key:function(i){return Object.keys(s)[i]||null},get length(){return Object.keys(s).length}}}["localStorage","sessionStorage"].forEach(function(n){try{window[n].length}catch(e){try{Object.defineProperty(window,n,{configurable:true,value:mem()})}catch(e2){}}})})();`

// htmlRewriter is the rewrite function prepareHTMLBody invokes on HTML bodies. It
// defaults to rewriteHTMLLimited and is a package var only so a test can
// substitute a failing rewriter to exercise the fail-closed path. Production
// never reassigns it.
var htmlRewriter = rewriteHTMLLimited

// hideParam is the inbound /proxy query parameter carrying author-configured
// hide-selectors (CR5). It is REPEATED — one CSS selector per value — rather than
// a single comma-joined value, because a CSS selector group can itself contain
// commas and significant whitespace (e.g. `a, b` or `div > .x`), so comma-joining
// would be ambiguous. ServeHTTP reads every `hide` value and threads them through to
// rewriteHTML, which applies them to the proxied HTML document.
const hideParam = "hide"

// maxHideSelectorLen bounds the byte length of any single hide selector (CR5,
// AC 31). A selector longer than this is rejected (skipped) before it ever reaches
// cascadia.Compile, so a pathological selector cannot drive unbounded compile work.
const maxHideSelectorLen = 1024

// maxHideSelectors caps the NUMBER of hide selectors honoured per request (CR5,
// AC 31). Selectors beyond this count are ignored, bounding the total matching work
// regardless of how many `hide` params a caller supplies.
const maxHideSelectors = 50

// hideStyleDecl is appended to a matched element's inline style attribute to hide
// it. `!important` overrides author/UA stylesheet rules; the leading separator is
// added conditionally (see applyHideSelectors) so an existing inline style is
// preserved and correctly terminated before this declaration.
const hideStyleDecl = "display:none!important"

// rewriteHTML parses html (charset-aware, per contentType + a <meta charset>)
// and prepares it for the panel's sandboxed srcdoc iframe (FR5):
//
//   - a <base href> pointing at the absolute upstream base is the first child
//     of <head> (an existing one is resolved and moved there), so URLs built at
//     runtime and CSS url() resolve against the upstream;
//   - every URL attribute in urlAttrs and every srcset candidate becomes an
//     absolute upstream URL, resolved against that base. Absolute URLs do not
//     depend on <base>, which Grafana's optional CSP (base-uri 'self') blocks;
//   - CSP and refresh <meta> tags and inline frame-busters are removed, and the
//     CR5 author hide-selectors are applied;
//   - upstream referrer policies are removed and, directly after <base>, a
//     <meta name="referrer" content="no-referrer"> and the sandbox
//     compatibility shim are injected, exactly once.
//
// The document is returned as UTF-8. All DOM mutation goes through the goquery
// and x/net/html APIs, whose renderer HTML-escapes attribute values — there is
// no string concatenation of HTML, so a hostile upstream URL (or a hostile
// hide-selector) cannot break out of an attribute. On any parse error it
// returns the error, and the proxy then refuses the page (502) rather than
// serve it un-rewritten. An empty or nil hideSelectors is a no-op.
// rewriteHTML applies no size limit; the proxy uses rewriteHTMLLimited.
func rewriteHTML(htmlBytes []byte, pageURL *url.URL, contentType string, hideSelectors []string) ([]byte, error) {
	return rewriteHTMLLimited(htmlBytes, pageURL, contentType, hideSelectors, 0)
}

// rewriteHTMLLimited is rewriteHTML with an output limit (VR-6): the rewritten
// document must not be longer than maxBytes, otherwise it returns
// errResponseTooLarge (413). Absolute URLs are longer than relative ones, and a
// long base URL multiplied over many short references could otherwise grow the
// document without bound, so URL rewriting also stops as soon as its growth
// exceeds the room left under maxBytes. maxBytes <= 0 means no limit.
func rewriteHTMLLimited(htmlBytes []byte, pageURL *url.URL, contentType string, hideSelectors []string, maxBytes int64) ([]byte, error) {
	// An empty body has nothing to rewrite (and charset.NewReader would surface an
	// EOF on it); return it unchanged rather than treating it as a parse failure.
	if len(htmlBytes) == 0 {
		return htmlBytes, nil
	}
	// Charset-aware decode: charset.NewReader sniffs the BOM / <meta charset> /
	// the supplied Content-Type and yields a UTF-8 reader, so the parser (which
	// assumes UTF-8) parses correctly regardless of the upstream encoding.
	reader, err := charset.NewReader(bytes.NewReader(htmlBytes), contentType)
	if err != nil {
		return nil, fmt.Errorf("charset reader: %w", err)
	}
	// The parse is bounded (htmlParseLimits): a document whose tree would grow
	// past the budget is refused as too large instead of exhausting memory.
	root, err := htmlparse.ParseDocument(reader, htmlParseLimits)
	if err != nil {
		if errors.Is(err, htmlparse.ErrLimitExceeded) {
			return nil, fmt.Errorf("%w: %w", errResponseTooLarge, err)
		}
		return nil, fmt.Errorf("parse html: %w", err)
	}
	doc := goquery.NewDocumentFromNode(root)

	base, baseNode := ensureBase(doc, pageURL)

	budget := newGrowthBudget(maxBytes, len(htmlBytes))
	rewriteURLAttrs(doc, base, budget)
	rewriteSrcset(doc, base, budget)
	if budget.exceeded {
		return nil, errResponseTooLarge
	}
	removeCSPMetas(doc)
	removeFrameBusters(doc)
	applyHideSelectors(doc, hideSelectors)
	removeReferrerPolicies(doc)
	injectSandboxCompat(doc, baseNode)

	return renderLimited(root, len(htmlBytes), maxBytes)
}

// htmlParseLimits bounds the parse of one proxied HTML document, whatever
// MaxResponseBytes is. A document that goes over a limit is refused with 413.
// See ai-state/streams/v1-stabilisation/memory-sizing.md for the sizing.
var htmlParseLimits = htmlparse.Limits{
	// The tokenizer's memory for one tag grows with its attributes (about 27
	// bytes per input byte for a tag of short, distinct attributes), so one
	// tag, text run, comment or inline script may be at most 2 MiB.
	MaxTokenBytes: 2 << 20,
	// The estimated tree (nodes, attributes, text, cloned formatting
	// elements). An ordinary 5 MiB page needs well under half of this.
	MaxTreeBytes: 48 << 20,
	// Text appends and attribute comparisons, which grow faster than the
	// input on hostile documents. Ordinary pages use almost none of this.
	MaxWork: 16 << 20,
}

// renderLimited renders the tree as HTML into a buffer and stops with
// errResponseTooLarge as soon as the output would exceed maxBytes, so an
// oversize document is never rendered in full first. sizeHint (the input
// length) pre-sizes the buffer. maxBytes <= 0 means no limit.
func renderLimited(root *html.Node, sizeHint int, maxBytes int64) ([]byte, error) {
	var buf bytes.Buffer
	grow := int64(sizeHint) + int64(sizeHint)/4 + 4096
	if maxBytes > 0 && grow > maxBytes {
		grow = maxBytes
	}
	buf.Grow(int(grow))
	if err := html.Render(&limitedWriter{buf: &buf, max: maxBytes}, root); err != nil {
		if errors.Is(err, errResponseTooLarge) {
			return nil, errResponseTooLarge
		}
		return nil, fmt.Errorf("render html: %w", err)
	}
	return buf.Bytes(), nil
}

// limitedWriter appends to buf and fails with errResponseTooLarge, writing
// nothing, when a write would take buf past max bytes (max <= 0: no limit).
type limitedWriter struct {
	buf *bytes.Buffer
	max int64
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if w.max > 0 && int64(w.buf.Len())+int64(len(p)) > w.max {
		return 0, errResponseTooLarge
	}
	return w.buf.Write(p)
}

// growthBudget bounds the bytes that URL rewriting may add to a document
// (VR-6). An unlimited budget allows any growth.
type growthBudget struct {
	limited   bool
	remaining int64
	exceeded  bool
}

// newGrowthBudget allows URL rewriting to grow a document of inputLen bytes up
// to maxBytes; maxBytes <= 0 means no limit.
func newGrowthBudget(maxBytes int64, inputLen int) *growthBudget {
	if maxBytes <= 0 {
		return &growthBudget{}
	}
	return &growthBudget{limited: true, remaining: maxBytes - int64(inputLen)}
}

// allow records that oldLen bytes are replaced with newLen bytes and reports
// whether the document is still within the budget. Once it returns false it
// keeps returning false.
func (b *growthBudget) allow(oldLen, newLen int) bool {
	if !b.limited {
		return true
	}
	if b.exceeded {
		return false
	}
	if grow := int64(newLen - oldLen); grow > 0 {
		b.remaining -= grow
		if b.remaining < 0 {
			b.exceeded = true
			return false
		}
	}
	return true
}

// ensureBase makes the document's first <base href> the first child of <head>
// and returns the effective base URL with that element. An existing base is
// resolved against pageURL (so a relative base still produces an absolute
// upstream base) and moved; a base whose href does not parse or does not
// resolve to http(s) (for example javascript:, which browsers ignore) is reset
// to pageURL. Without one, <base href="<pageURL>"> is created. pageURL is the
// final page URL: the proxy does not follow redirects itself, so each hop's
// /proxy request carries its own target and the last hop's target is the page.
func ensureBase(doc *goquery.Document, pageURL *url.URL) (*url.URL, *html.Node) {
	base := pageURL
	var node *html.Node
	if existing := documentBase(doc); existing.Length() > 0 {
		node = existing.Get(0)
		if href, ok := existing.Attr("href"); ok {
			if ref, err := url.Parse(strings.TrimSpace(href)); err == nil {
				if resolved := pageURL.ResolveReference(ref); isHTTPScheme(resolved.Scheme) {
					base = resolved
				}
			}
		}
		existing.SetAttr("href", base.String())
		node.Parent.RemoveChild(node)
	} else {
		node = &html.Node{
			Type:     html.ElementNode,
			Data:     "base",
			DataAtom: atom.Base,
			Attr:     []html.Attribute{{Key: "href", Val: base.String()}},
		}
	}

	// x/net/html always synthesises <head>; the fallbacks cover a document
	// without one.
	parent := doc.Get(0)
	if head := doc.Find("head").First(); head.Length() > 0 {
		parent = head.Get(0)
	} else if htmlEl := doc.Find("html").First(); htmlEl.Length() > 0 {
		parent = htmlEl.Get(0)
	}
	parent.InsertBefore(node, parent.FirstChild)
	return base, node
}

// documentBase returns the <base href> element that sets the document base URL
// in a browser: the first one in the HTML namespace that is not inside a
// <template>. A <base> in SVG or MathML content, or in template contents, does
// not change the base URL, so it is ignored. The selection is empty when the
// document has no such element.
func documentBase(doc *goquery.Document) *goquery.Selection {
	return doc.Find("base[href]").FilterFunction(func(_ int, s *goquery.Selection) bool {
		n := s.Get(0)
		if n.Namespace != "" {
			return false
		}
		for p := n.Parent; p != nil; p = p.Parent {
			if p.Type == html.ElementNode && p.DataAtom == atom.Template && p.Namespace == "" {
				return false
			}
		}
		return true
	}).First()
}

// injectSandboxCompat inserts <meta name="referrer" content="no-referrer"> and
// the sandbox compatibility shim directly after base, so they precede every
// upstream element and the shim runs before any page script. A copy of the
// shim already in the page (an upstream that is itself a proxied page) is
// removed first, so the shim appears exactly once. removeReferrerPolicies has
// already removed every other referrer meta.
func injectSandboxCompat(doc *goquery.Document, base *html.Node) {
	doc.Find("script").FilterFunction(func(_ int, s *goquery.Selection) bool {
		return s.Text() == sandboxCompatShim
	}).Remove()

	meta := &html.Node{
		Type:     html.ElementNode,
		Data:     "meta",
		DataAtom: atom.Meta,
		Attr: []html.Attribute{
			{Key: "name", Val: "referrer"},
			{Key: "content", Val: referrerPolicyNoReferrer},
		},
	}
	script := &html.Node{Type: html.ElementNode, Data: "script", DataAtom: atom.Script}
	script.AppendChild(&html.Node{Type: html.TextNode, Data: sandboxCompatShim})

	base.Parent.InsertBefore(meta, base.NextSibling)
	base.Parent.InsertBefore(script, meta.NextSibling)
}

// removeReferrerPolicies removes the upstream's own referrer policies so the
// injected no-referrer policy is the only one: every <meta name="referrer">
// (the last one processed would win) and every referrerpolicy attribute (it
// overrides the document policy for that element's request).
func removeReferrerPolicies(doc *goquery.Document) {
	doc.Find("meta[name]").Each(func(_ int, s *goquery.Selection) {
		if name, _ := s.Attr("name"); strings.EqualFold(strings.TrimSpace(name), "referrer") {
			s.Remove()
		}
	})
	doc.Find("[referrerpolicy]").RemoveAttr("referrerpolicy")
}

// rewriteURLAttrs makes every attribute in urlAttrs an absolute upstream URL.
// Values that absoluteURL rejects are left verbatim. It stops as soon as the
// growth budget is exceeded; the caller then fails the rewrite.
func rewriteURLAttrs(doc *goquery.Document, base *url.URL, budget *growthBudget) {
	for _, ua := range urlAttrs {
		doc.Find(ua.selector).EachWithBreak(func(_ int, s *goquery.Selection) bool {
			raw, ok := s.Attr(ua.attr)
			if !ok {
				return true
			}
			abs, ok := absoluteURL(raw, base)
			if !ok {
				return true
			}
			if !budget.allow(len(raw), len(abs)) {
				return false
			}
			s.SetAttr(ua.attr, abs)
			return true
		})
		if budget.exceeded {
			return
		}
	}
}

// isHTTPScheme reports whether scheme is http or https (case-insensitive).
func isHTTPScheme(scheme string) bool {
	return strings.EqualFold(scheme, "http") || strings.EqualFold(scheme, "https")
}

// absoluteURL resolves ref against base and returns the absolute URL when it is
// http or https. Empty refs, pure fragments (in-page anchors), unparseable refs
// and refs that resolve to another scheme (data:, blob:, mailto:, tel:,
// javascript:, about:, ...) return false so the caller leaves them verbatim. A
// fragment is kept (it matters for SVG sprites, media fragments and anchors on
// another page).
func absoluteURL(ref string, base *url.URL) (string, bool) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", false
	}
	resolved := base.ResolveReference(parsed)
	if !isHTTPScheme(resolved.Scheme) {
		return "", false
	}
	return resolved.String(), true
}

// rewriteSrcset rewrites every srcset-style attribute in srcsetAttrs candidate
// by candidate (see rewriteSrcsetValue). It stops as soon as the growth budget
// is exceeded; the caller then fails the rewrite.
func rewriteSrcset(doc *goquery.Document, base *url.URL, budget *growthBudget) {
	for _, sa := range srcsetAttrs {
		doc.Find(sa.selector).EachWithBreak(func(_ int, s *goquery.Selection) bool {
			raw, ok := s.Attr(sa.attr)
			if !ok || strings.TrimSpace(raw) == "" {
				return true
			}
			rewritten, ok := rewriteSrcsetValue(raw, base, budget)
			if !ok {
				return false
			}
			s.SetAttr(sa.attr, rewritten)
			return true
		})
		if budget.exceeded {
			return
		}
	}
}

// srcsetSpace is the HTML ASCII whitespace set used by the srcset grammar.
const srcsetSpace = " \t\n\f\r"

// rewriteSrcsetValue rewrites one srcset value. It follows the HTML srcset
// parsing rules: a candidate URL is a run of non-whitespace characters (it may
// contain commas, as CDN URLs such as ".../w_100,h_100/a.jpg" do; trailing
// commas end the candidate), and the descriptors that follow run to the next
// comma outside parentheses. Each http(s) candidate URL becomes absolute; other
// URLs (data:, ...) and every descriptor ("2x", "640w") are kept verbatim.
// Candidates are re-joined with ", ". It returns false, and stops, as soon as
// the growth budget is exceeded.
func rewriteSrcsetValue(srcset string, base *url.URL, budget *growthBudget) (string, bool) {
	var out []string
	rest := srcset
	for {
		rest = strings.TrimLeft(rest, srcsetSpace+",")
		if rest == "" {
			break
		}
		end := strings.IndexAny(rest, srcsetSpace)
		if end < 0 {
			end = len(rest)
		}
		candidate := rest[:end]
		rest = rest[end:]

		descriptor := ""
		if strings.HasSuffix(candidate, ",") {
			// A URL ending in commas has no descriptors.
			candidate = strings.TrimRight(candidate, ",")
		} else {
			depth, i := 0, 0
			for ; i < len(rest); i++ {
				switch rest[i] {
				case '(':
					depth++
				case ')':
					if depth > 0 {
						depth--
					}
				}
				if rest[i] == ',' && depth == 0 {
					break
				}
			}
			descriptor = strings.TrimSpace(rest[:i])
			rest = rest[i:]
		}

		if abs, ok := absoluteURL(candidate, base); ok {
			if !budget.allow(len(candidate), len(abs)) {
				return "", false
			}
			candidate = abs
		}
		if descriptor != "" {
			candidate += " " + descriptor
		}
		out = append(out, candidate)
	}
	return strings.Join(out, ", "), true
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

// applyHideSelectors applies the author-configured CR5 hide-selectors to the
// top-level proxied document: each VALID selector's matching elements have
// `display:none!important` APPENDED to their existing inline `style` attribute so
// they are hidden. An empty/nil hideSelectors is a complete no-op (no DOM touched),
// so the rendered output is byte-identical to a rewrite with no hide-selectors.
//
// VALIDATION (security-critical, AC 31). Each selector is rejected/skipped — never
// fatal — when it (a) exceeds maxHideSelectorLen, or (b) fails cascadia.Compile
// (malformed / unsupported selector syntax). The NUMBER of selectors honoured is
// also capped at maxHideSelectors to bound total matching work. Rejected selectors
// are simply ignored; valid selectors in the same request are unaffected.
//
// MARKUP-INJECTION SAFETY (the core of AC 31). The hide is NOT applied by injecting
// a <style> block built from selector text: a cascadia-VALID attribute selector such
// as `[x="</style><script>…"]` would, if emitted verbatim into a raw-text <style>
// element, close the style element and inject live markup. Instead this pass uses
// goquery to FIND matching elements (the compiled cascadia.Sel is used purely for
// MATCHING, never serialised) and SetAttr to write only the fixed, selector-free
// hideStyleDecl into each matched element's style attribute. goquery's renderer
// HTML-escapes attribute values, and the selector TEXT never enters the rendered
// markup at all, so markup injection is impossible by construction.
//
// LIMITATION (documented, accepted). Only elements PRESENT in the server-rendered
// HTML are hidden. Elements the page's client-side JS adds to the DOM later are not
// covered — we do not execute JS — which is acceptable: the panel can additionally
// hide via client-side CSS where it has same-origin DOM access (out of scope here).
func applyHideSelectors(doc *goquery.Document, hideSelectors []string) {
	if len(hideSelectors) == 0 {
		return
	}
	applied := 0
	for _, raw := range hideSelectors {
		if applied >= maxHideSelectors {
			break // over-count cap: ignore any further selectors
		}
		sel := strings.TrimSpace(raw)
		if sel == "" || len(sel) > maxHideSelectorLen {
			continue // empty or oversized: skip without affecting valid selectors
		}
		matcher, err := cascadia.Compile(sel)
		if err != nil {
			continue // invalid CSS selector: skip (logged/ignored, never fatal)
		}
		applied++
		doc.FindMatcher(matcher).Each(func(_ int, s *goquery.Selection) {
			hideElement(s)
		})
	}
}

// hideElement appends hideStyleDecl to s's inline style attribute, preserving any
// existing inline style. The existing style is terminated with a ';' before the
// appended declaration (added only when the existing style does not already end in
// one) so the two declarations stay distinct. The value is written via SetAttr, so
// goquery's renderer escapes it on output — no selector text or HTML is ever
// concatenated into the markup.
func hideElement(s *goquery.Selection) {
	existing := strings.TrimSpace(s.AttrOr("style", ""))
	if existing == "" {
		s.SetAttr("style", hideStyleDecl)
		return
	}
	if !strings.HasSuffix(existing, ";") {
		existing += ";"
	}
	s.SetAttr("style", existing+hideStyleDecl)
}
