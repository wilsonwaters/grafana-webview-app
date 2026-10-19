package plugin

// Redirect handling for /proxy (CR4): the hop-depth control parameter and the
// rewrite of a redirect's Location to a relative /proxy URL that the browser
// follows, re-entering the full security pipeline for the next hop.

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/wilsonwaters/webview/pkg/security"
)

// errRedirectDepthExceeded is the sentinel ModifyResponse returns when a 3xx
// redirect would push the hop depth past cfg.MaxRedirects (CR4). It is raised
// BEFORE any followable Location is emitted so the browser never re-enters the
// proxy for the over-cap hop; proxyErrorHandler maps it to a clean 502 with the
// redirect-loop reason. Matched with errors.Is, never by string.
var errRedirectDepthExceeded = errors.New("proxy: redirect depth exceeds maximum")

// errRedirectBlocked is the sentinel ModifyResponse returns when a 3xx redirect's
// resolved Location host fails the allowlist pre-check (CR4 defense-in-depth). It
// blocks the denied hop at the redirect step rather than emitting a rewritten
// Location the browser would have to be refused on re-request; proxyErrorHandler
// maps it to a 403 with the allowlist reason. Matched with errors.Is, never by
// string.
var errRedirectBlocked = errors.New("proxy: redirect target host is not allowlisted")

// wvRedirParam is the reserved, internal query parameter the proxy uses to carry
// the redirect-hop depth across browser-driven re-entries (CR4). It lives ONLY on
// the proxy URL (the /proxy?url=… Location the panel's fetch follows), NEVER on
// the upstream request: the outbound request is rebuilt purely
// from the `url` target param (see serveProxy's Rewrite / buildTargetURL), so this
// control param is never forwarded upstream. ServeHTTP reads it inbound to learn the
// current hop depth; ModifyResponse writes depth+1 onto the rewritten Location.
//
// CR4 deliberately does NOT follow redirects server-side. Instead a 3xx response
// has its Location rewritten to a proxy URL so the BROWSER re-enters the proxy for
// the next hop, where the FULL security pipeline (SF2 scheme/port, SF3 allowlist,
// SF5 rate/concurrency, SF4 resolve-then-dial IP blocklist) re-runs on that hop.
// The browser therefore only ever talks to the proxy, never the upstream directly,
// so a denied destination is refused (403) BEFORE the browser contacts it.
const wvRedirParam = "_wvredir"

// redirectDepthOf reads the reserved redirect-hop depth control param from the
// inbound proxy request. A missing, blank, non-numeric, or negative value yields
// 0 (the first hop) — a caller-supplied bogus or oversized value cannot break the
// pipeline: it simply parses to 0 or a large int that immediately trips the depth
// cap. The param is internal and never forwarded upstream.
func redirectDepthOf(req *http.Request) int {
	raw := strings.TrimSpace(req.URL.Query().Get(wvRedirParam))
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// isRedirectStatus reports whether status is one of the HTTP redirect codes that
// carry a Location the proxy must handle (CR4). Only the Location-bearing 3xx
// codes are handled — 304 Not Modified and 300 Multiple Choices are deliberately
// excluded: they are not single-target redirects the browser auto-follows, so
// their (rare/absent) Location is left untouched and the response passes through.
func isRedirectStatus(status int) bool {
	switch status {
	case http.StatusMovedPermanently, // 301
		http.StatusFound,             // 302
		http.StatusSeeOther,          // 303
		http.StatusTemporaryRedirect, // 307
		http.StatusPermanentRedirect: // 308
		return true
	default:
		return false
	}
}

// handleRedirect implements CR4 redirect handling for a proxied response, run in
// ModifyResponse AFTER the size guard and framing/header strip but BEFORE the
// body step. The proxy does NOT follow redirects server-side; instead it
// rewrites the Location to a /proxy URL so the browser (the panel's fetch)
// re-enters the proxy for the next hop, where the FULL security pipeline
// re-validates that hop.
//
// It returns (handled, err):
//   - handled==false, err==nil: not a (followable) redirect — caller continues to
//     the normal body step. This covers a non-3xx status, a 3xx with no Location,
//     a 3xx with an unparseable Location, and a 3xx whose Location resolves to a
//     non-http(s) scheme (data:/mailto:/…), which is left as-is rather than
//     rewritten into a non-fetchable proxy URL.
//   - handled==true, err==nil: the redirect was rewritten in place. Location is
//     now the relative reference proxy?url=<next hop>&_wvredir=<depth+1>, plus
//     the request's hide selectors (see buildRedirectProxyURL). The body step is
//     skipped (a redirect body is an inert courtesy page).
//   - err!=nil: the redirect is REFUSED at the redirect step. err is
//     errRedirectDepthExceeded (depth cap reached / MaxRedirects==0 ⇒ redirects
//     disabled) or errRedirectBlocked (resolved Location host fails the SF2+SF3
//     allowlist pre-check). proxyErrorHandler maps these to 502/403 respectively;
//     the ReverseProxy emits the denial BEFORE any followable Location is written,
//     so the browser never re-enters the proxy for the refused hop.
//
// SECURITY: the allowlist pre-check here is defense-in-depth — it blocks a denied
// hop at the redirect step. The AUTHORITATIVE per-hop IP-blocklist gate remains
// SF4's resolve-then-dial in the transport, which runs when the browser re-requests
// the rewritten proxy URL (we deliberately do NOT add a DNS lookup here). Because
// the browser only ever talks to the proxy — never the upstream directly — a
// destination that fails allowlist (here) or IP-blocklist (on re-request) is
// refused before the browser can contact it.
func (p *proxyHandler) handleRedirect(resp *http.Response, target *url.URL, depth int, hideSelectors []string) (bool, error) {
	if resp == nil || !isRedirectStatus(resp.StatusCode) {
		return false, nil
	}
	loc := strings.TrimSpace(resp.Header.Get("Location"))
	if loc == "" {
		// A redirect status with no Location is malformed; nothing to rewrite or
		// follow — pass it through unchanged.
		return false, nil
	}

	// Resolve Location against the CURRENT hop's target to an absolute URL. A
	// relative Location ("/next", "../x") resolves against target; an absolute one
	// is used as-is.
	ref, err := url.Parse(loc)
	if err != nil {
		// An unparseable Location cannot be turned into a followable proxy URL;
		// strip it and pass the (now Location-less) redirect through rather than
		// emitting something the browser would chase to a non-proxied destination.
		resp.Header.Del("Location")
		return false, nil
	}
	abs := target.ResolveReference(ref)

	// Only http/https redirects are fetchable through the proxy. A non-http(s)
	// resolved scheme (data:, mailto:, tel:, javascript:, …) is left as-is: it is
	// not an SSRF vector the proxy fetches, and rewriting it into a proxy URL would
	// be wrong. Pass the response through unchanged.
	switch strings.ToLower(abs.Scheme) {
	case "http", "https":
	default:
		return false, nil
	}

	// Depth cap (Completion Criterion). The current hop arrived at `depth`; the
	// redirect would make the NEXT hop depth+1. If we have already reached the
	// configured maximum we refuse rather than emit a followable Location. With
	// MaxRedirects==0 this trips on the very first redirect ⇒ redirects disabled.
	if depth >= p.cfg.MaxRedirects {
		return false, errRedirectDepthExceeded
	}

	// Defense-in-depth allowlist pre-block (SF2 normalise + SF3 match) on the
	// resolved Location host. A denied hop is refused here, at the redirect step,
	// rather than being rewritten into a proxy URL that the re-request would refuse.
	hostname, herr := hostnameOf(abs.String())
	if herr != nil {
		return false, errRedirectBlocked
	}
	if match := security.MatchHostname(hostname, p.allowlist); !match.Allowed {
		return false, errRedirectBlocked
	}

	// Rewrite Location to a relative /proxy URL the panel's fetch will re-enter,
	// carrying the encoded absolute next-hop target, the incremented hop depth
	// and the request's hide selectors.
	resp.Header.Set("Location", buildRedirectProxyURL(abs.String(), depth+1, hideSelectors))
	return true, nil
}
