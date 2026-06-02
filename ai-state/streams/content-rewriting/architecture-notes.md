# Content-Rewriting — Architecture Notes

Durable cross-task decisions (from the CR2 design pass, orchestrator-approved). CR3/CR4/CR5 must honour these.

## Subresource URL scheme (Q9 — RESOLVED: query-encoded)
- Rewritten subresource refs → `/api/plugins/wilsonwaters-webview-app/resources/proxy-resource?url=<percent-encoded absolute URL>` (same shape as top-level `/proxy?url=`).
- **CR3** recovers the target with a single `req.URL.Query().Get("url")` and runs the SAME security pipeline as `/proxy`: SF2 `ValidateURL` → SF3 `MatchHostname` → SF5 rate-limit/concurrency → SF4 resolve-then-dial. Subresource safety is enforced HERE at fetch time — the CR2 rewriter deliberately does NOT allowlist/IP-check (would be redundant + couple it to settings).
- Navigation refs (`a`, `area`, `iframe`, GET `form[action]`) → `/proxy?url=…` (re-enters top-level HTML rewriting). Subresource refs (`img/script/link/source/video/audio/track/object/embed`, `srcset`) → `/proxy-resource?url=…`.
- Build URLs via the Go URL API (`url.Values.Encode()`), never string concat (escaping/anti-injection).

## base-href + resolution
- Resolve each ref against the page's effective base (original fetched URL combined with any existing `<base href>`) to an absolute target, then rewrite to an **origin-absolute** `/api/.../proxy-resource?url=` (or `/proxy?url=`) URL. Because rewritten values start `/api/...`, the browser resolves them against the Grafana origin and `<base href>` does NOT affect them — so injecting a base cannot corrupt rewritten refs.
- Inject `<base href="<original page absolute URL>">` (or set an existing base to the absolute effective base) as a BACKSTOP for refs CR2 doesn't rewrite (runtime-JS URLs, CSS `url()`, out-of-set attrs) so they degrade to the correct upstream origin rather than the Grafana origin.
- Only rewrite refs whose resolved scheme is http/https; leave `data:`/`blob:`/`mailto:`/`tel:`/`javascript:`/`#fragment` verbatim.

## Page URL source (CR2 vs CR4)
- CR2 uses the INITIAL validated `target *url.URL` (captured in `serveProxy`'s `ModifyResponse` closure) as the page base.
- **CR4** (redirects) must substitute the FINAL resolved hop as the page base when it lands, and rewrite `Location` headers to proxy URLs + re-validate every hop (allowlist + IP) capping depth (default 3).

## Frame-buster removal (Q11 — RESOLVED): see OPEN-QUESTIONS Q11. Inline-script-only, comparison-AND-navigation marker pair, whole-script removal, false-negative bias.

## Meta removal
- Remove `<meta http-equiv>` for `content-security-policy` / `content-security-policy-report-only` and `refresh` (conservative — a refresh would navigate the panel out of the proxy). `X-Frame-Options` is header-only (not honoured in meta) — inert, leave.

## Seam / structure
- CR1 left a `// CR2:` seam in `prepareHTMLBody`. **CR2 restructures it so rewriting runs on ALL HTML** (CR1 currently only rewrites gzip HTML; plain HTML hits an early return — must be fixed). Rewrite logic in new `pkg/plugin/rewrite.go` (`rewriteHTML(html []byte, pageURL *url.URL, contentType string) ([]byte, error)`). Charset-aware parse via `x/net/html/charset`, emit UTF-8. Dep: `github.com/PuerkitoBio/goquery` (no regex HTML parsing).
- **Degradation:** on rewrite/parse error, serve the DECODED ORIGINAL HTML (200, log + metric), NOT 502 — security gates already ran; a parse quirk shouldn't be a gateway error. (gzip-decode failure still → 502, unchanged.)

## Out of scope (CR2): the `/proxy-resource` endpoint itself (CR3), redirects (CR4), hide-selectors (CR5), POST-form proxying, inline `style`/`on*`/`<style>` `url()` rewriting, server-side JS execution.
