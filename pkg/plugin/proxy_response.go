package plugin

// The /proxy response path: the response size limit, gzip decoding, the
// buffered body and HTML rewrite step, and the response header policy
// (framing, cookies, origin-level policy and CORS headers).

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// errResponseTooLarge is the sentinel ModifyResponse returns when an upstream
// response is KNOWN (via a non-negative Content-Length) to exceed
// MaxResponseBytes, BEFORE any body byte has been streamed to the client. The
// ReverseProxy has not yet written status/headers at that point, so the
// ErrorHandler can cleanly emit a 413. Matched with errors.Is, never by string.
var errResponseTooLarge = errors.New("proxy: response body exceeds maximum allowed size")

// strippedResponseHeaders are the exact-match headers removed from every proxied
// response, in addition to the framing strip (X-Frame-Options / CSP
// frame-ancestors) P1 already performs. Header.Del canonicalises the key, so these
// match case-insensitively and remove ALL values of a multi-valued header (e.g.
// every Set-Cookie line). The goal is that the proxied page cannot set state on,
// or impose origin-level security policy against, the viewer's browser via the
// Grafana origin: the proxy is a stateless, unauthenticated fetch and the response
// is served from Grafana's own origin. Each removal is justified inline.
var strippedResponseHeaders = []string{
	// State-setting — must never write cookies into the viewer's Grafana origin.
	"Set-Cookie", // upstream session/tracking cookies (all values removed)

	// Origin-level security policy the upstream tries to pin onto the Grafana
	// origin — would persist beyond this single fetch and affect Grafana itself.
	"Strict-Transport-Security",   // HSTS: would force HTTPS-only on the Grafana origin
	"Public-Key-Pins",             // HPKP: would pin cert keys against the Grafana origin
	"Public-Key-Pins-Report-Only", // HPKP report-only sibling: same pinning policy, report mode
	"Clear-Site-Data",             // could wipe the viewer's cookies/storage/cache for the Grafana origin

	// Client-side navigation — a browser honours "Refresh: 0; url=..." like a
	// redirect, but it bypasses the per-hop redirect re-validation (CR4) that
	// only Location gets (VR-5). The <meta http-equiv=refresh> twin is removed
	// by the rewriter.
	"Refresh",
}

// corsHeaderPrefix is swept (case-insensitively) off every proxied response
// (R1 D2). /proxy and /check-frameable are same-origin only: the panel calls
// them from the Grafana page, so they send no Access-Control-* headers at all.
// An upstream's own CORS headers must not pass through either: with
// Access-Control-Allow-Origin set to another site and
// Access-Control-Allow-Credentials: true, an allowlisted upstream could let
// that site read proxy responses with the viewer's Grafana session.
const corsHeaderPrefix = "access-control-"

// deleteHeadersWithPrefix deletes every header whose lower-cased name starts
// with prefix. It collects the keys first so the map is not mutated while it is
// ranged over.
func deleteHeadersWithPrefix(h http.Header, prefix string) {
	var keys []string
	for key := range h {
		if strings.HasPrefix(strings.ToLower(key), prefix) {
			keys = append(keys, key)
		}
	}
	for _, key := range keys {
		delete(h, key)
	}
}

// enforceResponseSize applies the MaxResponseBytes cap to an upstream response.
//
// Clean path (declared length): if the upstream sent a non-negative
// Content-Length that already exceeds the limit, return errResponseTooLarge.
// ModifyResponse runs before the ReverseProxy writes any status/header, so the
// ErrorHandler can still emit a clean 413 with no bytes leaked.
//
// Undeclared or chunked length (Content-Length -1): wrap resp.Body in a reader
// that fails with errResponseTooLarge once more than maxBytes are read.
// prepareHTMLBody reads the whole body inside ModifyResponse, before the
// ReverseProxy has written any status, so an oversize chunked body also ends as
// a clean 413 and is never delivered as a truncated 200 (VR-7).
func enforceResponseSize(resp *http.Response, maxBytes int64) error {
	if maxBytes <= 0 {
		return nil
	}
	if resp.ContentLength >= 0 && resp.ContentLength > maxBytes {
		return errResponseTooLarge
	}
	resp.Body = newLimitedBody(resp.Body, maxBytes)
	return nil
}

// limitedBody wraps an upstream response body and caps the total number of
// bytes that may be read at limit. Reading the (limit+1)th byte fails the copy
// with errResponseTooLarge instead of silently truncating, so an oversized
// chunked/undeclared body is rejected rather than served partial-but-200.
type limitedBody struct {
	rc        io.ReadCloser
	remaining int64 // bytes still allowed before the limit is breached
}

// newLimitedBody caps rc at limit total bytes. It permits exactly limit bytes
// and errors only when the upstream tries to send MORE than limit.
func newLimitedBody(rc io.ReadCloser, limit int64) *limitedBody {
	return &limitedBody{rc: rc, remaining: limit}
}

func (l *limitedBody) Read(p []byte) (int, error) {
	if l.remaining < 0 {
		return 0, errResponseTooLarge
	}
	// Read up to remaining+1 bytes: the extra byte lets us detect an over-limit
	// body (a full read of remaining bytes that is followed by more data).
	max := l.remaining + 1
	if int64(len(p)) > max {
		p = p[:max]
	}
	n, err := l.rc.Read(p)
	l.remaining -= int64(n)
	if l.remaining < 0 {
		return int(l.remaining + int64(n)), errResponseTooLarge
	}
	return n, err
}

func (l *limitedBody) Close() error { return l.rc.Close() }

// contentEncodingGzip is the only Content-Encoding the proxy accepts (CR1).
// Outbound requests advertise only gzip (see stripRequestHeaders), and gzip is
// the only coding whose decoded size the proxy checks. A response in any other
// coding (deflate, br, zstd, or a list of codings) is refused with 502, because
// its decoded size could not be held to MaxResponseBytes (VR-6).
const contentEncodingGzip = "gzip"

// errUnsupportedEncoding is returned for a response whose Content-Encoding is
// not gzip or identity. proxyErrorHandler maps it to 502.
var errUnsupportedEncoding = errors.New("proxy: upstream response uses an unsupported Content-Encoding")

// responseEncoding returns "" for an unencoded (or identity) response and
// contentEncodingGzip for gzip or x-gzip. Any other coding, or more than one
// coding, returns errUnsupportedEncoding.
func responseEncoding(h http.Header) (string, error) {
	var codings []string
	for _, v := range h.Values("Content-Encoding") {
		for _, c := range strings.Split(v, ",") {
			if c = strings.ToLower(strings.TrimSpace(c)); c != "" && c != "identity" {
				codings = append(codings, c)
			}
		}
	}
	switch {
	case len(codings) == 0:
		return "", nil
	case len(codings) == 1 && (codings[0] == contentEncodingGzip || codings[0] == "x-gzip"):
		return contentEncodingGzip, nil
	default:
		return "", errUnsupportedEncoding
	}
}

// isHTMLContentType reports whether a Content-Type header value names an HTML
// document. Matching is case-insensitive and tolerates parameters such as a
// "; charset=utf-8" suffix and surrounding whitespace, so "text/html",
// "TEXT/HTML; charset=UTF-8", and "text/html ;charset=utf-8" all match. Only
// the bare "text/html" media type is treated as HTML; anything else (JSON,
// images, application/xhtml+xml, etc.) is not, so it passes through unchanged.
func isHTMLContentType(contentType string) bool {
	// Cut off any parameters (charset, boundary, ...) at the first ';'.
	mediaType := contentType
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = mediaType[:i]
	}
	return strings.EqualFold(strings.TrimSpace(mediaType), "text/html")
}

// prepareHTMLBody reads the whole response body into memory, bounded, and
// rewrites it when it is HTML. It runs in ModifyResponse AFTER the P4 size
// guard, the P1/P3 framing/header strip and the CR4 redirect step, and BEFORE
// the ReverseProxy sends the status. So every failure below reaches the client
// as a clean error status, and no response is delivered truncated or with a
// decoded size above maxBytes (VR-6/VR-7):
//
//   - A bodiless status (1xx, 204, 304) or an empty body passes through
//     without a Content-Encoding, whatever the upstream declared.
//   - More than maxBytes on the wire (chunked or a lying Content-Length) ⇒
//     errResponseTooLarge ⇒ 413.
//   - A Content-Encoding other than gzip ⇒ errUnsupportedEncoding ⇒ 502.
//   - Non-HTML: the body and headers pass through unchanged (a gzip body stays
//     compressed, so JSON/images/etc. stay byte-identical), but a gzip body is
//     first decoded to io.Discard to prove that its DECODED size is within
//     maxBytes (gzip-bomb guard). Over ⇒ 413; malformed gzip ⇒ 502.
//   - HTML: gzip is decoded (bounded), then rewriteHTMLLimited rewrites the
//     document (CR2, FR5); the rewritten document must also fit in maxBytes,
//     otherwise 413. Content-Encoding is dropped and the charset set to UTF-8.
//   - Content-Length is set to the length actually delivered.
//
// A rewrite failure other than the size limit (for example a document nested
// deeper than the parser accepts) fails closed: errRewriteFailed ⇒ 502 (the
// error handler logs the cause). The un-rewritten page is never served.
// maxBytes <= 0 disables every bound. pageURL is the validated target of this
// hop; it is the base for relative-URL resolution. hideSelectors are the CR5
// author hide-selectors; rewriteHTMLLimited validates and applies them. ctx is
// the request context: an HTML body waits for a rewrite slot until it ends.
func prepareHTMLBody(ctx context.Context, resp *http.Response, maxBytes int64, pageURL *url.URL, hideSelectors []string) error {
	if resp == nil || resp.Body == nil {
		return nil
	}
	// A status that has no body (1xx, 204, 304) has nothing to decode or
	// rewrite. Its Content-Encoding is dropped: on a 304 it would update the
	// browser's cached copy, which for HTML is the decoded, rewritten page.
	if !bodyAllowedForStatus(resp.StatusCode) {
		discardBody(resp)
		return nil
	}

	raw, err := readBounded(resp.Body, resp.ContentLength, maxBytes)
	// Close the upstream body whatever the outcome.
	if cerr := resp.Body.Close(); cerr != nil && err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	// An empty body has nothing to decode or rewrite either. A gzip decode of
	// zero bytes would fail, so the Content-Encoding is dropped and the empty
	// response passes through.
	if len(raw) == 0 {
		resp.Header.Del("Content-Encoding")
		setBufferedBody(resp, nil)
		return nil
	}

	encoding, err := responseEncoding(resp.Header)
	if err != nil {
		return err
	}
	gzipped := encoding == contentEncodingGzip

	if !isHTMLContentType(resp.Header.Get("Content-Type")) {
		if gzipped {
			if err := gzipDecodedWithin(raw, maxBytes); err != nil {
				return err
			}
		}
		setBufferedBody(resp, raw)
		return nil
	}

	// HTML is decoded, parsed, rewritten and rendered while holding a rewrite
	// slot, so only a few documents are in memory as parse trees at once,
	// however many requests are in flight.
	release, err := acquireRewriteSlot(ctx)
	if err != nil {
		return err
	}
	defer release()

	decoded := raw
	if gzipped {
		if decoded, err = decodeGzipBounded(bytes.NewReader(raw), maxBytes); err != nil {
			return err
		}
	}

	// CR2/FR5: rewrite the HTML. Any failure fails closed: the original page
	// without the injected base, referrer policy and shim, and with its
	// frame-busters and CSP metas, must never reach the panel.
	rewritten, rerr := htmlRewriter(decoded, pageURL, resp.Header.Get("Content-Type"), hideSelectors, maxBytes)
	if rerr != nil {
		if errors.Is(rerr, errResponseTooLarge) {
			return rerr
		}
		return fmt.Errorf("%w: %w", errRewriteFailed, rerr)
	}
	// The rewriter emits UTF-8; update the Content-Type charset so the client
	// decodes it correctly regardless of the upstream's declared charset.
	resp.Header.Set("Content-Type", "text/html; charset=utf-8")

	// The body is now plain HTML: drop the encoding header so the client does
	// not try to decode it again.
	if gzipped {
		resp.Header.Del("Content-Encoding")
	}
	setBufferedBody(resp, rewritten)
	return nil
}

// errRewriteFailed wraps a rewriter error other than the size limit, for
// example a document nested deeper than the parser accepts.
// proxyErrorHandler maps it to 502.
var errRewriteFailed = errors.New("proxy: upstream HTML could not be rewritten")

// maxConcurrentHTMLRewrites is the number of HTML documents this plugin
// process decodes, parses, rewrites and renders at the same time, across all
// app instances. A parse tree is many times larger than its document, so this,
// not MaxConcurrentRequests, bounds the memory of the rewrite step. Other
// requests wait for a slot until their request deadline. See
// ai-state/streams/v1-stabilisation/memory-sizing.md.
const maxConcurrentHTMLRewrites = 2

// htmlRewriteSlots is the semaphore behind maxConcurrentHTMLRewrites.
var htmlRewriteSlots = make(chan struct{}, maxConcurrentHTMLRewrites)

// acquireRewriteSlot waits for a rewrite slot and returns the function that
// frees it. It returns ctx's error if ctx ends first (a timeout is then a 504).
func acquireRewriteSlot(ctx context.Context) (func(), error) {
	select {
	case htmlRewriteSlots <- struct{}{}:
		return func() { <-htmlRewriteSlots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// readBounded reads r to the end. More than maxBytes returns
// errResponseTooLarge without reading further; maxBytes <= 0 means no limit.
// sizeHint (the declared Content-Length, or -1) pre-sizes the buffer so a body
// of known length is read without the garbage of a growing buffer.
func readBounded(r io.Reader, sizeHint, maxBytes int64) ([]byte, error) {
	var buf bytes.Buffer
	if sizeHint > 0 && (maxBytes <= 0 || sizeHint <= maxBytes) {
		// ReadFrom wants bytes.MinRead free bytes before each read, including
		// the final read that returns io.EOF.
		buf.Grow(int(sizeHint) + bytes.MinRead)
	}
	if maxBytes > 0 {
		r = io.LimitReader(r, maxBytes+1)
	}
	if _, err := buf.ReadFrom(r); err != nil {
		return nil, err
	}
	if maxBytes > 0 && int64(buf.Len()) > maxBytes {
		return nil, errResponseTooLarge
	}
	return buf.Bytes(), nil
}

// setBufferedBody replaces the response body with body and sets the framing to
// match: Content-Length is the buffered length and upstream trailers are
// dropped (a buffered body is sent with a fixed length).
func setBufferedBody(resp *http.Response, body []byte) {
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Trailer = nil
	resp.Header.Del("Trailer")
	if bodyAllowedForStatus(resp.StatusCode) {
		resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	}
}

// discardBody drops the body of a response whose body the client never sees
// (a redirect the browser follows), without reading it.
func discardBody(resp *http.Response) {
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
	resp.Header.Del("Content-Encoding")
	setBufferedBody(resp, nil)
}

// bodyAllowedForStatus reports whether a response with this status may carry a
// body (RFC 9110: not 1xx, 204 or 304).
func bodyAllowedForStatus(status int) bool {
	switch {
	case status >= 100 && status <= 199:
		return false
	case status == http.StatusNoContent, status == http.StatusNotModified:
		return false
	}
	return true
}

// decodeGzipBounded gzip-decompresses r, reading at most maxBytes decoded bytes
// (maxBytes <= 0 disables the bound). If the decoded stream would exceed
// maxBytes it returns errResponseTooLarge without decoding further — the
// decompression-bomb guard.
func decodeGzipBounded(r io.Reader, maxBytes int64) ([]byte, error) {
	var buf bytes.Buffer
	if err := copyGzipBounded(&buf, r, maxBytes); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// gzipDecodedWithin decodes raw to io.Discard and reports errResponseTooLarge
// when the decoded size exceeds maxBytes, or the gzip error when raw is not a
// valid gzip stream. It allocates no decoded buffer.
func gzipDecodedWithin(raw []byte, maxBytes int64) error {
	return copyGzipBounded(io.Discard, bytes.NewReader(raw), maxBytes)
}

// copyGzipBounded decodes the gzip stream r into dst. It reads one byte past
// maxBytes so a body of exactly maxBytes is allowed while anything larger is
// detected as an overflow (errResponseTooLarge).
func copyGzipBounded(dst io.Writer, r io.Reader, maxBytes int64) error {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	// Best-effort close; any read error is reported by io.Copy below.
	defer func() { _ = gr.Close() }()

	src := io.Reader(gr)
	if maxBytes > 0 {
		src = io.LimitReader(gr, maxBytes+1)
	}
	n, err := io.Copy(dst, src)
	if err != nil {
		return err
	}
	if maxBytes > 0 && n > maxBytes {
		return errResponseTooLarge
	}
	return nil
}

// frameAncestorsDirective is the CSP directive that controls who may frame a
// page. Removing it (along with X-Frame-Options) is what makes a proxied,
// otherwise-frameable page embeddable in a Grafana panel.
const frameAncestorsDirective = "frame-ancestors"

// stripFramingHeaders sanitises the proxied response. It removes the headers that
// prevent the page from being framed (X-Frame-Options outright; the
// frame-ancestors directive inside Content-Security-Policy while leaving all other
// CSP directives intact — P1) AND deletes the dangerous incoming response headers
// in strippedResponseHeaders (Set-Cookie, HSTS, HPKP, Clear-Site-Data — P3) so the
// upstream cannot set cookies on, or pin origin-level security policy onto, the
// Grafana origin the response is served from. It also removes every upstream
// Access-Control-* header (R1 D2). Body/base-tag/frame-buster rewriting is OUT OF
// SCOPE here — that is content-rewriting (see rewriteHTML).
func stripFramingHeaders(resp *http.Response) error {
	// P3: drop dangerous incoming response headers (state-setting + origin-level
	// security policy). Header.Del canonicalises the key and removes every value,
	// so multi-valued headers (e.g. several Set-Cookie lines) are fully cleared.
	for _, name := range strippedResponseHeaders {
		resp.Header.Del(name)
	}

	// X-Frame-Options: delete entirely. There is no partial form to preserve.
	resp.Header.Del("X-Frame-Options")

	// Content-Security-Policy (and the report-only variant): drop only the
	// frame-ancestors directive, keeping every other directive. CSP may appear
	// as multiple header values; rewrite each.
	for _, headerName := range []string{"Content-Security-Policy", "Content-Security-Policy-Report-Only"} {
		values := resp.Header.Values(headerName)
		if len(values) == 0 {
			continue
		}
		rewritten := make([]string, 0, len(values))
		for _, v := range values {
			if nv := removeFrameAncestors(v); nv != "" {
				rewritten = append(rewritten, nv)
			}
		}
		resp.Header.Del(headerName)
		for _, v := range rewritten {
			resp.Header.Add(headerName, v)
		}
	}

	// R1 D2: no Access-Control-* header of any kind leaves the proxy.
	deleteHeadersWithPrefix(resp.Header, corsHeaderPrefix)
	return nil
}

// removeFrameAncestors returns the CSP header value with any frame-ancestors
// directive removed, preserving the order and content of all other directives.
// Directives are separated by ';'. Matching on the directive name is
// case-insensitive (CSP directive names are case-insensitive).
func removeFrameAncestors(csp string) string {
	parts := strings.Split(csp, ";")
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		// A directive is "name value...". Compare only the name token.
		name := trimmed
		if i := strings.IndexAny(trimmed, " \t"); i >= 0 {
			name = trimmed[:i]
		}
		if strings.EqualFold(name, frameAncestorsDirective) {
			continue // drop this directive
		}
		kept = append(kept, trimmed)
	}
	return strings.Join(kept, "; ")
}
