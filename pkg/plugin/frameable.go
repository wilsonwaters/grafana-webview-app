package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/wilsonwaters/webview/pkg/security"
)

// checkFrameablePath is the resource path the frameability-check endpoint is
// registered under.
const checkFrameablePath = "/check-frameable"

// recommendedMode values returned in the /check-frameable response. They MUST
// match the F2 load-mode enum used on the frontend (`direct`/`proxy`/`auto`):
// the panel maps a verdict straight onto its load mode, so a direct-frameable
// page is rendered with a plain iframe and a blocked/ambiguous page is routed
// through the backend proxy (which strips framing headers).
const (
	recommendedModeDirect = "direct"
	recommendedModeProxy  = "proxy"
)

// frameableResponse is the Q7 response contract returned with HTTP 200 once the
// security pipeline PASSES. Pipeline denials are surfaced as proxy-style HTTP
// error codes (see checkFrameable) and never reach this body.
type frameableResponse struct {
	// Frameable is true only when the upstream imposes NO framing restriction we
	// can detect (no blocking X-Frame-Options, no blocking CSP frame-ancestors).
	Frameable bool `json:"frameable"`
	// Reason is a short human-readable explanation of the verdict, naming the
	// blocker when framing is denied.
	Reason string `json:"reason"`
	// RecommendedMode is the load mode the panel should use: "direct" when the
	// page is frameable, "proxy" otherwise (blocked or ambiguous/error).
	RecommendedMode string `json:"recommendedMode"`
}

// checkFrameableHandler adapts a proxyHandler to serve the /check-frameable
// endpoint (FR1). It carries NO state of its own: it shares the SAME settings,
// allowlist, rate limiter and SF4 secure-dialer transport as the owning
// proxyHandler, and runs the IDENTICAL pre-fetch security pipeline as /proxy
// (SF2 scheme/port → SF3 allowlist → SF5 rate/concurrency → SF4 resolve-then-
// dial at fetch time). It differs from /proxy only in what it does once the
// pipeline passes: a single non-following GET to inspect framing headers, then
// a JSON verdict — it never streams or rewrites the upstream body.
type checkFrameableHandler struct {
	p *proxyHandler
}

// ServeHTTP implements the /check-frameable endpoint. It runs the shared
// security pipeline (reusing proxyHandler's validation sequence and denial
// mapping) and, on success, performs the server-side framing check.
func (h checkFrameableHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	h.p.checkFrameable(w, req)
}

// checkFrameable implements the /check-frameable endpoint contract (Q7).
//
// It runs the EXACT same pre-fetch security pipeline as /proxy's serve — parse
// `url` → SF2 hostname normalise → SF3 allowlist match (empty allowlist denies)
// → SF2 full ValidateURL with the matched domain's extra ports → SF5 rate-limit
// then concurrency Acquire — reusing writeDenial / the reasonStatus table so the
// denial status codes are identical to /proxy (400 bad scheme/port/missing url,
// 403 not-allowlisted, 429 rate/concurrency). On ANY pipeline denial it writes
// the mapped HTTP error and returns; no fetch happens.
//
// Once every gate passes it performs ONE server-side GET through the SF4
// transport (so DNS-resolve-then-dial + IP blocklist still apply at connect
// time), with redirects NOT followed, inspects the framing headers, and returns
// HTTP 200 with the {frameable, reason, recommendedMode} JSON verdict. A fetch
// error/timeout, or a redirect with no framing verdict, is treated as
// proxy-recommended (the proxy is more robust): still HTTP 200, frameable:false.
func (p *proxyHandler) checkFrameable(w http.ResponseWriter, req *http.Request) {
	// CORS preflight / method handling mirrors /proxy: this is a GET endpoint.
	setCORSHeaders(w.Header())
	if req.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if req.Method != http.MethodGet {
		writeDenial(w, denialReasonMethod, "method not allowed")
		return
	}

	rawTarget := req.URL.Query().Get("url")
	if strings.TrimSpace(rawTarget) == "" {
		writeDenial(w, denialReasonBadRequest, "missing required 'url' query parameter")
		return
	}

	// SF2 (pre): normalise the hostname for the allowlist match, learning the
	// matched domain's extra ports before the authoritative ValidateURL.
	hostname, herr := hostnameOf(rawTarget)
	if herr != nil {
		writeDenial(w, denialReasonScheme, "invalid target URL: "+security.ReasonOf(herr))
		return
	}

	// SF3: allowlist match (empty/nil allowlist denies everything, fail-closed).
	match := security.MatchHostname(hostname, p.allowlist)
	if !match.Allowed {
		writeDenial(w, denialReasonAllowlist, "target host is not allowlisted")
		return
	}

	// SF2: full validation of scheme/userinfo/host/port, allowing the matched
	// domain's extra ports. All failures are client errors ⇒ 400.
	validated, err := security.ValidateURL(rawTarget, match.Options.AllowedPorts)
	if err != nil {
		writeDenial(w, denialReasonScheme, "invalid target URL: "+security.ReasonOf(err))
		return
	}

	// SF5: rate-limit tiers (per instance, per domain), then the concurrency cap.
	instanceID := instanceIDFromContext(req)
	if allowed, reason := p.rateLimiter.Allow(instanceID, validated.Hostname); !allowed {
		writeDenial(w, denialReasonRateLimit, "rate limit exceeded: "+reason)
		return
	}
	release, ok := p.rateLimiter.Acquire()
	if !ok {
		writeDenial(w, denialReasonConcurrency, "too many concurrent proxy requests")
		return
	}
	defer release()

	// Rebuild the upstream URL from the validated components + original
	// path/query, exactly like /proxy, so the host we contact matches what the
	// pipeline approved.
	target, perr := buildTargetURL(rawTarget, validated)
	if perr != nil {
		writeDenial(w, denialReasonBadRequest, "invalid target URL")
		return
	}

	// Every gate passed: perform the framing check. SF4's resolve-then-dial +
	// IP blocklist run inside p.transport at connect time, so a blocked/metadata
	// IP fails the dial here and is treated (like any fetch error) as
	// proxy-recommended rather than a hard denial — the verdict is still HTTP 200.
	//
	// check-frameable shares p.transport with /proxy, so the matched domain's
	// AllowPrivateIP opt-in must apply here too (otherwise an opted-in domain that
	// /proxy can fetch would be reported un-frameable because the check dial was
	// refused). The policy carries NO OnPrivatePermit: the permit audit/metric
	// belongs to the proxy serve path; a frameability probe is a read-only check.
	pol := security.Policy{AllowPrivate: match.Options.AllowPrivateIP}
	resp := p.checkFrameableVerdict(req.Context(), target.String(), pol)
	writeFrameableResponse(w, resp)
}

// frameableTimeout returns the total deadline for the framing-check fetch,
// derived from the same RequestTimeoutSec setting that bounds a /proxy request.
func (p *proxyHandler) frameableTimeout() time.Duration {
	return time.Duration(p.cfg.RequestTimeoutSec) * time.Second
}

// checkFrameableVerdict performs the single server-side fetch and returns the
// framing verdict. It is only called AFTER the security pipeline has approved
// targetURL. The fetch uses p.transport (SF4 secure dialer) with a total
// timeout and does NOT follow redirects, so the framing headers inspected are
// those of the first response from the validated target.
//
// Verdict rules (Q7):
//   - fetch error/timeout (incl. an SF4-blocked dial) ⇒ frameable:false, proxy.
//   - a redirect (3xx) reached with no framing verdict ⇒ frameable:false, proxy
//     (the proxy handles redirects with per-hop re-validation; the direct iframe
//     cannot, so route through the proxy).
//   - X-Frame-Options DENY/SAMEORIGIN, or CSP frame-ancestors blocking ⇒
//     frameable:false, proxy, naming the blocker.
//   - otherwise ⇒ frameable:true, direct.
func (p *proxyHandler) checkFrameableVerdict(ctx context.Context, targetURL string, pol security.Policy) frameableResponse {
	client := &http.Client{
		Transport: p.transport,
		Timeout:   p.frameableTimeout(),
		// Do NOT follow redirects: inspect the response we get. Re-validating a
		// redirect hop is proxy/CR4 territory; FR1 is a single-request check.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// A GET is safer than HEAD for the framing check: some servers omit framing
	// headers on HEAD. We read NOTHING of the body (only headers are needed) and
	// always close it so the connection can be released.
	//
	// Attach the matched domain's relaxation Policy to the base context BEFORE the
	// timeout wrap, so it survives and the SF4 dialer reads it back via
	// security.WithPolicy at connect time — keeping the AllowPrivateIP opt-in
	// consistent with /proxy. With pol.AllowPrivate==false this is the strict
	// default.
	reqCtx, cancel := context.WithTimeout(security.WithPolicy(ctx, pol), p.frameableTimeout())
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodGet, targetURL, nil)
	if err != nil {
		return frameableResponse{
			Frameable:       false,
			Reason:          "upstream unreachable: " + err.Error(),
			RecommendedMode: recommendedModeProxy,
		}
	}
	// Apply the same outbound header hygiene spirit as the proxy: identify
	// honestly and leak nothing about the Grafana instance/viewer.
	httpReq.Header.Set("User-Agent", proxyUserAgent)
	httpReq.Header.Set("Accept", proxyAccept)

	resp, err := client.Do(httpReq)
	if err != nil {
		return frameableResponse{
			Frameable:       false,
			Reason:          "upstream unreachable: " + err.Error(),
			RecommendedMode: recommendedModeProxy,
		}
	}
	// Close the body without reading it: the verdict comes from headers only.
	defer func() { _ = resp.Body.Close() }()

	// A redirect status with redirects disabled gives no framing verdict for the
	// final destination; recommend the proxy, which re-validates each hop (CR4).
	if isRedirectStatus(resp.StatusCode) {
		return frameableResponse{
			Frameable:       false,
			Reason:          "upstream redirects; cannot verify framing of the final destination",
			RecommendedMode: recommendedModeProxy,
		}
	}

	return frameingVerdictFromHeaders(resp.Header)
}

// frameingVerdictFromHeaders inspects the framing-relevant response headers and
// returns the Q7 verdict.
//
// X-Frame-Options: a value of DENY or SAMEORIGIN (case-insensitive) blocks
// framing in a third-party (Grafana) origin.
//
// CSP frame-ancestors (conservative rule): the backend does NOT know the
// Grafana origin, so it cannot prove a source list permits it. Therefore a
// present frame-ancestors directive blocks UNLESS its source list contains the
// wildcard `*` (which allows any origin). 'none' blocks outright. An ABSENT
// frame-ancestors directive does not block via CSP.
func frameingVerdictFromHeaders(h http.Header) frameableResponse {
	if blocked, reason := xFrameOptionsBlocks(h.Get("X-Frame-Options")); blocked {
		return frameableResponse{Frameable: false, Reason: reason, RecommendedMode: recommendedModeProxy}
	}

	// Inspect every CSP header value (the header may be repeated or multi-valued).
	for _, csp := range h.Values("Content-Security-Policy") {
		if blocked, reason := cspFrameAncestorsBlocks(csp); blocked {
			return frameableResponse{Frameable: false, Reason: reason, RecommendedMode: recommendedModeProxy}
		}
	}

	return frameableResponse{
		Frameable:       true,
		Reason:          "no framing restrictions",
		RecommendedMode: recommendedModeDirect,
	}
}

// xFrameOptionsBlocks reports whether an X-Frame-Options header value blocks
// framing in a third-party origin. DENY and SAMEORIGIN both block (we are not
// same-origin with the upstream). Matching is case-insensitive and tolerant of
// surrounding whitespace. An empty or unrecognised value (e.g. a legacy
// ALLOW-FROM, which modern browsers ignore) does not block here.
func xFrameOptionsBlocks(value string) (bool, string) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "DENY":
		return true, "X-Frame-Options: DENY"
	case "SAMEORIGIN":
		return true, "X-Frame-Options: SAMEORIGIN"
	default:
		return false, ""
	}
}

// cspFrameAncestorsBlocks parses a single Content-Security-Policy header value
// for a frame-ancestors directive and applies the conservative blocking rule
// (see frameingVerdictFromHeaders). It returns (true, reason) when the directive
// is present and does NOT permit any origin via `*`.
func cspFrameAncestorsBlocks(csp string) (bool, string) {
	for _, directive := range strings.Split(csp, ";") {
		directive = strings.TrimSpace(directive)
		if directive == "" {
			continue
		}
		// Split the directive into its name token and source list.
		fields := strings.Fields(directive)
		if len(fields) == 0 {
			continue
		}
		if !strings.EqualFold(fields[0], frameAncestorsDirective) {
			continue
		}
		sources := fields[1:]
		// A bare `frame-ancestors` with no sources is equivalent to 'none'.
		if len(sources) == 0 {
			return true, "CSP frame-ancestors: 'none'"
		}
		for _, src := range sources {
			s := strings.TrimSpace(src)
			if s == "*" {
				// Wildcard permits any origin (including Grafana's) ⇒ not blocked.
				return false, ""
			}
			if strings.EqualFold(strings.Trim(s, "'"), "none") {
				return true, "CSP frame-ancestors: 'none'"
			}
		}
		// A source list that does not include `*`: conservatively blocked, since
		// the backend cannot confirm it includes the (unknown) Grafana origin.
		return true, "CSP frame-ancestors: restrictive source list"
	}
	return false, ""
}

// writeFrameableResponse writes the verdict as HTTP 200 JSON. An encode failure
// (the response writer broke) is surfaced as a 500 — but the pipeline already
// passed, so this is an I/O edge case, not a security denial.
func writeFrameableResponse(w http.ResponseWriter, resp frameableResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	// Headers/status are already written; a mid-encode failure (broken connection)
	// is unrecoverable, so the best-effort encode result is intentionally ignored
	// — there is nothing further we can send to the client at this point.
	_ = json.NewEncoder(w).Encode(resp)
}
