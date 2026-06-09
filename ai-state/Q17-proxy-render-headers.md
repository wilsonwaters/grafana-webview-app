# Q17 — In-panel Proxy-mode render blocked by Grafana resource-route headers

**Status:** CRITICAL follow-up, not yet fixed. Tracked as GitHub issue (see board) + OPEN-QUESTIONS Q17.
**Discovered:** 2026-06-03, during the live render test after frameability (FR1–FR4) completed.
**This document is self-contained** so it can be handed to a separate AI agent to resolve independently.

---

## 1. What this plugin is (context)

`wilsonwaters-webview-app` is a **Grafana app plugin** (id `wilsonwaters-webview-app`) that bundles a
nested **panel plugin** (id `wilsonwaters-webview-panel`). The panel renders an external web page inside
a Grafana dashboard panel, in a sandboxed `<iframe>`, scaled to a configured viewport (CSS transform).
Repo: `wilsonwaters/grafana-webview-app`. Frontend = React/TS (`src/`); backend = Go
(`pkg/plugin/`, `pkg/security/`), built with mage, run via Grafana's plugin SDK `httpadapter`/`ServeMux`.

The plugin has **two render modes** (panel option `loadMode`: `auto` | `direct` | `proxy`):
- **Direct mode:** the iframe `src` is the external URL directly. The browser frames the third-party site
  cross-origin. Works for sites that allow framing. Unaffected by this bug.
- **Proxy mode:** for sites that BLOCK framing (`X-Frame-Options`/CSP `frame-ancestors`), the plugin's Go
  backend fetches the page server-side, **strips the framing headers, rewrites the HTML** (resolves URLs,
  routes subresources through the proxy, removes frame-buster JS + CSP `<meta>`), and serves it from
  Grafana's own origin so it can be framed. The target use case is a public weather radar (BOM radar) that
  sends `X-Frame-Options`.

The backend proxy is **complete and security-hardened**: endpoint `/proxy?url=<enc>` runs a full pipeline
(allowlist → URL validate → rate-limit → DNS-resolve-then-dial with an IP blocklist, rebinding-safe),
strips framing + identity + dangerous response headers, enforces body-size/timeout, audit-logs, exposes
Prometheus metrics, rewrites HTML (goquery), serves subresources via `/proxy-resource?url=<enc>`, and
re-validates redirects. **This is all runtime-verified working** — see §5.

## 2. The frontend wiring that's affected (FR4)

`src/panels/webview/components/WebViewPanel.tsx` (view-mode render) and
`src/panels/webview/loadMode.ts` decide the iframe `src`:
- `resolveLoadMode(opts)` → `'direct' | 'proxy'` (auto → `detectedMode ?? 'direct'`).
- `buildProxySrc(opts)` → `` `${config.appSubUrl}/api/plugins/wilsonwaters-webview-app/resources/proxy?url=<enc>` `` plus a `&hide=<enc>` per hide-selector.
- In proxy mode the panel sets the iframe **`src` directly to that resource-route URL**:
  `src = mode === 'proxy' ? buildProxySrc(opts) : opts.url`.
The iframe sandbox attribute is `sandbox="allow-scripts allow-same-origin"`, `referrerPolicy="no-referrer"`.

## 3. THE PROBLEM

Grafana's **core HTTP layer stamps two response headers on EVERY `/api/plugins/*/resources/*` route**,
regardless of what the plugin handler returns:
- `X-Frame-Options: deny`
- `Content-Security-Policy: sandbox`

(Confirmed empirically: these appear even on the plugin's trivial `/resources/health` JSON handler, which
sets no such headers. They are framework-injected. Our backend DOES correctly strip the *upstream* page's
`X-Frame-Options` via `stripFramingHeaders` in `pkg/plugin/proxy.go` — that code is correct and verified;
the problem is Grafana RE-adding its own headers on the resource route afterwards.)

**Consequences for proxy mode (the iframe `src` → `/resources/proxy?url=…`):**
1. **`X-Frame-Options: deny` blocks the iframe from framing the resource route at all** — even though it is
   same-origin (dashboard and resource route are both the Grafana origin). XFO `deny` forbids ALL framing.
   → the panel iframe shows nothing / a browser frame-block error.
2. **`Content-Security-Policy: sandbox` sandboxes the served document**, disabling its JavaScript (and more).
   In the live test the rendered NeverSSL page showed its "JavaScript appears to be disabled" banner. For
   the BOM radar (which needs JS to animate the radar loop) this alone would break it even if framing worked.

Net: **in-panel Proxy mode does not currently render.** Direct mode is unaffected (it frames the external
URL cross-origin, never a Grafana resource route).

This is likely **config-sensitive**: Grafana's `security.allow_embedding` setting (default `false`) controls
the `X-Frame-Options` Grafana emits app-wide; but the `CSP: sandbox` on *plugin resource* responses is a
separate, deliberate Grafana plugin-sandboxing behavior that `allow_embedding` does not turn off. So even
with `allow_embedding = true` the CSP-sandbox (JS-disabled) problem likely remains for the resource route.

## 4. Recommended fix (to evaluate/confirm) — fetch-then-`srcdoc`

Instead of pointing the iframe `src` at the resource route, in **proxy mode**:
1. **Fetch** the rewritten HTML with `getBackendSrv().get('/api/plugins/.../resources/proxy', { url, hide })`
   (a *fetch* is NOT subject to `X-Frame-Options` — XFO only governs framing, not XHR/fetch reads; and the
   resource's `CSP: sandbox` does not constrain the *fetching* document).
2. Set the iframe's **`srcdoc`** to that HTML string. The iframe then renders an `about:srcdoc` document
   whose framing is not gated by XFO, and whose CSP/sandbox is governed by the iframe's OWN `sandbox`
   attribute (which we control), not Grafana's resource-route CSP.
3. **Subresources** (`<img>/<link>/<script>` rewritten to absolute `/api/plugins/.../resources/proxy-resource?url=…`)
   load as normal subresource requests — XFO and the resource-route `CSP: sandbox` do **not** apply to
   img/script/link loads (only to framing/the document context). So they keep working.

### ⚠️ Security nuance that MUST be designed + reviewed (the crux)
The current direct-mode iframe uses `sandbox="allow-scripts allow-same-origin"`. For a **cross-origin** `src`
(direct mode) `allow-same-origin` gives the frame the *external site's* origin — fine. But for **`srcdoc`**,
`allow-same-origin` makes the frame inherit the **embedding (Grafana) origin** — so attacker-influenced
proxied content (we are, after all, rendering an arbitrary external page) would get **same-origin access to
the Grafana page** (cookies, localStorage, the Grafana session) = a serious XSS/privilege-escalation vector.

So the srcdoc iframe must **NOT** combine `allow-scripts` + `allow-same-origin`. Options to weigh:
- `sandbox="allow-scripts"` (no `allow-same-origin`): scripts run but the frame is an opaque/unique origin —
  safe from Grafana-origin access. **This is likely the right answer** (the BOM radar's JS runs against its
  own opaque origin; it doesn't need Grafana's origin). Verify the radar's JS works under an opaque origin
  (it may use relative/absolute URLs which are already proxy-rewritten, so it shouldn't need same-origin).
- Keep `allow-same-origin` OFF and accept that any same-origin-dependent proxied JS won't work (acceptable).
- A stricter CSP injected into the rewritten HTML to constrain what the proxied content can do.

### Alternatives to also consider (the stakeholder did not pick an approach)
- **Require/​document `security.allow_embedding = true`** + investigate whether any Grafana setting relaxes
  the plugin-resource `CSP: sandbox`. (Likely insufficient alone due to the CSP-sandbox-on-resources behavior,
  but worth confirming per Grafana version ≥12.3.)
- A **dedicated non-`/resources/` delivery** path — not generally available to plugins (all plugin backend
  HTTP is under `/api/plugins/<id>/resources/`), so probably not viable.
- Confirm exact behavior across the supported Grafana range (12.3.x – 13.x + nightly); the header behavior
  may differ by version.

## 5. How it was verified working (so you know the backend is sound)
Live in dev Grafana 12.4.0: `curl /api/plugins/wilsonwaters-webview-app/resources/proxy?url=http://neverssl.com/`
→ HTTP 200, rewritten HTML (`<base href>` injected), audit log `status=200 bytes=3994`, metric
`webview_proxy_requests_total{status="200"}`. A headless-Chromium navigation to that URL rendered the
**fully-styled** page (screenshot: `docs/screenshots/runtime/proxy-render-neverssl.png`) — but with JS
disabled by the `CSP: sandbox` (the "JavaScript appears to be disabled" banner), which is the §3 problem.
(Note: the sandbox MITMs outbound HTTPS, so use plain-HTTP `http://neverssl.com` to test; the real BOM
radar is HTTPS and 502s *in this sandbox only* — not a code issue.)

## 6. Files to touch for the fix
- `src/panels/webview/components/WebViewPanel.tsx` — proxy-mode branch: fetch HTML via `getBackendSrv`,
  render via `srcdoc`, set the safe `sandbox` flags (NOT `allow-same-origin` for srcdoc). Handle loading/
  error states; keep direct mode as-is (iframe `src`).
- `src/panels/webview/loadMode.ts` — `buildProxySrc` already builds the URL/params; may add a helper that
  returns the fetch path + params separately for `getBackendSrv().get(path, params)`.
- Tests: `WebViewPanel.test.tsx` (mock `getBackendSrv`, assert srcdoc set from fetched HTML, sandbox flags,
  loading/error). Consider an e2e that asserts the panel iframe actually renders proxied content.
- No backend change needed (the backend already serves correct rewritten HTML). Possibly document the
  required Grafana `allow_embedding` setting in the admin/config docs (DR3/DR4).

## 7. How to reproduce / test the fix in the dev env
- `docker compose up -d --build` (RUNBOOK); plugin at http://localhost:3000, anon auth.
- Temp-allowlist a plain-HTTP site (`neverssl.com`) in `provisioning/plugins/apps.yaml`
  (`jsonData.allowedDomains: [{domain: neverssl.com, options:{allowSubdomains:true}}]`), restart.
- Add a Web View panel, set `loadMode: proxy`, `url: http://neverssl.com`. Today: blank/blocked frame.
  After the fix: the page renders in-panel (and with `sandbox="allow-scripts"`, its JS runs).
