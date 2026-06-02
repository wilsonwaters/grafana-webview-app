# Master Plan — Backend Proxy & Hardening (`proxy`)

## Goal

Deliver a security-hardened `/proxy` endpoint built on `httputil.ReverseProxy` that
fetches a page through the full security pipeline, strips framing and dangerous headers in
both directions, enforces request/response resource limits, and emits audit logs and
Prometheus metrics. Vertical slice target: an allowlisted, framable-but-blocked page loads
in the panel via proxy mode by the end of P1, hardened progressively after.

## Task list

| # | Task | Size | Depends on |
|---|------|------|-----------|
| P1 | Core proxy slice: `/proxy?url=...` via `httputil.ReverseProxy` with `Director` running the security pipeline (SF2 → SF3 → SF5 → SF4) and `ModifyResponse` stripping `X-Frame-Options` and modifying CSP `frame-ancestors`; `ErrorHandler` for clean failures; permissive CORS on responses. Allowlisted page renders in-panel via proxy mode. (AC 13, 14 partial, 15) | M | security-foundation SF2–SF5; frameability FR4 (mode wiring) |
| P2 | Outgoing request header stripping: remove `Cookie`, `Authorization`, `X-Grafana-*` and any auth headers; set conservative `User-Agent` and `Accept`; stateless/unauthenticated. (AC 26) | M | P1 |
| P3 | Incoming response header stripping beyond framing: `Set-Cookie`, `Strict-Transport-Security`, `Public-Key-Pins`, `Clear-Site-Data`. (AC 27) | S | P1 |
| P4 | Resource limits: max response body size (default 5 MiB → 413), per-request connection + total timeout (default 10 s), enforced via the dialler/transport and a limited reader. (AC 24) | M | P1 |
| P5 | Audit logging: structured info-level log per proxy request with target URL, source Grafana user (if identifiable), status, response size, duration. (AC 28) | M | P1, P4 |
| P6 | Prometheus metrics: requests by status code, denials by reason (allowlist / IP blocklist / rate limit / size limit), in-flight gauge, request duration histogram; exposed on the plugin's metrics endpoint. (AC 29) | M | P1, P5 |
| P7 | Rate-limit + denial wiring to HTTP responses: 403 for allowlist/blocklist denials, 429 for rate-limit, 413 for size, 400 for bad scheme — each feeding the right metric/denial reason. (AC 17 enforcement, 25) | S | P1, P6 |

## Integration points

- Consumes every `security-foundation` library; first full assembly of the pipeline.
- Reads limits/allowlist from the foundation F3 settings schema.
- `content-rewriting` extends `ModifyResponse` (P1) with goquery HTML rewriting and adds the
  subresource endpoint that reuses this same pipeline.
- The proxy `src` URL produced here is what `panel-core` view mode and `frameability` FR4 select.
- Metrics/denial-reason taxonomy (P6/P7) is asserted by `testing-cicd` security suite.

## Out of scope

- HTML body rewriting (base tag, URL rewrite, frame-buster/CSP-meta removal) and subresource
  proxying and redirect handling — all in `content-rewriting`.
- Frameability detection (`frameability`).
- The detailed written threat model document (`docs-release`).
- Security regression test suite (`testing-cicd`); this stream ships Go unit tests per task.

## Open questions

- Subresource URL scheme: how `/proxy` rewrites point at `/proxy-resource` (query-encoded
  absolute URL vs path-embedded) — must be agreed with content-rewriting. Blocks P1/CR2.
  (See OPEN-QUESTIONS.)
- Whether per-request timeout should be split (connect vs total) as two settings or one.
  Blocks P4.

## Changelog

- Initialised at project kickoff (planning). No tasks dispatched yet.
