# Master Plan — Content Rewriting & Subresources (`content-rewriting`)

## Goal

Make proxied HTML pages actually render: resolve relative URLs, route subresources through
the proxy, strip frame-busting JS and CSP meta tags, and re-validate redirects on every
hop. Vertical slice target: a real allowlisted blocked-framing page (e.g. a public weather
radar) renders with working CSS/images/JS in the panel.

## Task list

| # | Task | Size | Depends on |
|---|------|------|-----------|
| CR1 | gzip handling + HTML detection in `ModifyResponse`: decode gzip responses, detect HTML by content-type before rewriting, pass non-HTML through unchanged. | M | proxy P1 |
| CR2 | goquery HTML rewriting slice: inject `<base href>`, rewrite absolute and relative `src`/`href` to route subresources through `/proxy-resource`, remove CSP `<meta>` tags, remove common frame-buster JS patterns. No regex parsing of HTML. (AC 14) | L | CR1; proxy P1 |
| CR3 | `/proxy-resource?url=...` subresource endpoint: serves CSS/JS/images through the **same** security pipeline (SF2 → SF3 → SF5 → SF4) and the same header policy as `/proxy`; streamed, size-limited. (AC 14, 18 partial) | M | CR2; security-foundation SF2–SF5 |
| CR4 | Redirect handling: cap redirect depth (default 3); re-validate every hop's target against allowlist + IP blocklist; rewrite `Location` headers to proxy URLs; block redirects into denied destinations at the redirect step. (AC 23) | M | CR2 |
| CR5 | Hide-selector + CSS-selector safety: apply author hide-selectors to proxied HTML via goquery; strictly validate selectors so they cannot inject markup. (AC 31) | M | CR2 |

## Integration points

- Extends `proxy` P1's `ModifyResponse`; CR3 reuses the `proxy` security pipeline verbatim so
  allowlist enforcement applies equally to `/proxy-resource` (AC 18).
- Subresource URL scheme must match the agreement with `proxy` (see shared open question).
- `panel-core` PC5 hide-selectors are only fully effective on same-origin proxied content
  produced here (cross-origin direct iframes cannot be DOM-rewritten).
- Redirect re-validation (CR4) and selector safety (CR5) are asserted by `testing-cicd`.

## Out of scope

- The core proxy fetch, header stripping, limits, logging, metrics (`proxy`).
- Server-side execution of proxied JavaScript (explicit non-goal — frame-busters are removed
  by static rewriting only).
- E2E rendering verification across browsers (`testing-cicd`).

## Open questions

- Subresource URL scheme (shared with `proxy` P1): query-encoded absolute URL vs path-embedded.
  Blocks CR2/CR3. (See OPEN-QUESTIONS.)
- How aggressively to strip "frame-buster" JS without breaking legitimate page scripts — needs a
  bounded, documented pattern set. Blocks CR2. (See OPEN-QUESTIONS.)

## Changelog

- Initialised at project kickoff (planning).
- **CR1 (#93) merged** — gzip decode + HTML-detection in `ModifyResponse`; non-HTML passthrough; the
  `// CR2:` rewrite seam. Security: `Accept-Encoding: gzip` pinned outbound to stop net/http's transparent
  unbounded auto-decompress; single decode bounded by `MaxResponseBytes` (gzip-bomb → 413). deflate/br
  out of scope (pass through).
- **CR2 (#94) merged** — goquery HTML rewriting per the approved design (Q9 query-encoded, Q11
  frame-buster marker-pair set; see `architecture-notes.md`). Rewrites subresource/navigation URLs,
  injects/fixes `<base href>`, removes CSP/refresh meta + inline frame-busters; restructured the seam so
  ALL HTML is rewritten; goquery-escaped (XSS-safe); degrades to original on rewrite error. 92.7%.
- **CR3 (#37) — in flight** — `/proxy-resource` endpoint serving the rewritten subresource URLs through
  the SAME pipeline/header policy as `/proxy` (no rewrite, Content-Type preserved, size-limited). After
  CR3, an allowlisted framing-blocked page renders. Then CR4 (redirects) + CR5 (hide-selectors).
