# R1 — Proxy-mode render on stock Grafana: findings

**Task:** R1 (research spike, v1.0.0 stabilisation). **Date:** 2026-10-18.
**Branch:** `worktree-agent-a5053ef85e23ff292` (based on `release/v1.0.0` @ `6b2224d`).
**Inputs:** `ai-state/Q17-proxy-render-headers.md`, master plan "Key research finding".

## 1. Short answer

**Can proxy mode render with running scripts and working subresources on stock Grafana in all three browsers?**

- **With the current subresource design (`/proxy-resource` URLs loaded by the framed page): NO.** Chromium and
  Firefox never send the Grafana session cookie on requests made by a sandboxed (opaque-origin) document, with
  `cookie_samesite` = `lax` (default) or `strict`. Every `/proxy-resource` request returns 401.
  The current rewriter also has a separate bug (D1, section 9). Its `/api/plugins/...` URLs resolve against the
  injected `<base href>` and go to the upstream host, not to Grafana.
- **With approach B (fetch, then `srcdoc`, `sandbox="allow-scripts"`) and subresources loaded directly from the
  upstream: YES.** The page renders, inline and external scripts run, CSS and images load, and the sandbox holds.
  Observed in Chromium 148, Firefox 150 and WebKit 26.4 on Grafana 13.2.4 and 12.4.13, on Windows and Linux
  browser builds. Panel-side inlining of proxied subresources as `data:` URLs also works in all three.
- **Exception:** if Grafana runs with `[security] content_security_policy = true`, the inherited CSP blocks the
  page's scripts and stylesheets in all three browsers (Q4).

**Recommendation:** approach **B** with `sandbox="allow-scripts"` (exact value). The backend emits absolute
upstream URLs for subresources and injects a referrer meta tag and a small storage shim. Details are in section 10.

## 2. Test setup

| Item | Value |
|------|-------|
| Grafana | `grafana/grafana:13.2.4` (commit `0e79aef`) and `grafana/grafana:12.4.13` (commit `6367d91`), plain `docker run`, unmodified |
| Auth | `GF_AUTH_ANONYMOUS_ENABLED=false`. Viewer user `viewer`, logged in with `POST /login` (sets `grafana_session`). |
| Plugin | This branch, frontend built in `node:24` (Linux), backend `mage build:linux`. Experimental `X-Allow-Embedding: allow` on `/proxy` (section 11). |
| Browsers | Playwright 1.60.0: Chromium 148.0.7778.96, Firefox 150.0.2, WebKit 26.4. Windows builds; Linux builds (`mcr.microsoft.com/playwright:v1.60.0-noble`) as a cross-check. |
| Grafana address | LAN address `http://192.168.2.196:<port>` (not `localhost`, see note below). HTTPS with a self-signed certificate for the `cookie_samesite=none` run. |
| Test page | nginx `r1-upstream` (only Grafana can reach it) and `r1-upstream-pub` (Grafana and browser can reach it, `192.168.2.196:3180`). The page has one stylesheet, one classic external script, one module script, one image, one image built at runtime by JS, and an inline script that writes its results into its own DOM and posts them to the parent. |
| Real pages | `https://example.com/`, `https://www.bom.gov.au/products/IDR663.loop.shtml` (now redirects, section 6), `https://reg.bom.gov.au/products/IDR663.loop.shtml` (legacy radar loop). |
| Method | Log in, open `/dashboards`, then use `page.evaluate` to insert the iframe the way the panel would. Results come from the frame DOM, Playwright network events, Grafana request logs (`userId`, `status`, `error="cannot authenticate request"`) and the plugin audit log (`user=viewer`). The audit log and Grafana log decide "cookie sent": Playwright does not report the `Cookie` header reliably in WebKit. |

Note on `localhost`: on `http://localhost` Chromium blocked approach A's subresources with a Local Network
Access "loopback" permission error. All matrix runs therefore use the LAN address.

Note on subresource URLs: because of defect D1, the as-is rewritten HTML sends every subresource to the upstream
host. To test what each approach does with `/proxy-resource`, the main matrix rewrites the `/proxy` response in
Playwright (`page.route`) so that the `/api/plugins/...` URLs carry the Grafana origin. This simulates a fixed
rewriter, and Grafana's response headers are kept. Section 4 shows the as-is output separately.

## 3. Q1 — headers with `X-Allow-Embedding: allow`

`curl -i` with basic auth on both versions. The result is the same for 12.4.13 and 13.2.4:

| Route | `X-Frame-Options` | `Content-Security-Policy` |
|-------|-------------------|---------------------------|
| `/proxy` (plugin sets `X-Allow-Embedding: allow`) | **absent** | `sandbox` (still present) |
| `/proxy-resource` (header not set) | `deny` | `sandbox` |
| `/health` (header not set) | `deny` | `sandbox` |

Browser control: framing an HTML document from a route without the header (`/proxy-resource?url=<html page>`) is
refused in all three browsers ("Refused to display ... 'X-Frame-Options' to 'deny'"). With the header, all
three browsers frame `/proxy`.

## 4. Q2 — Grafana 13.2.4, `cookie_samesite = lax`, subresources through `/proxy-resource`

The cells come from observation. "401" is confirmed in the Grafana log as `userId=0 ... cannot authenticate
request`. "200" is confirmed in the plugin audit log as `user=viewer`.

| Approach | Browser | (i) Renders | (ii) Inline script | (ii) External script | (iii) CSS / JS / IMG via `/proxy-resource` | Session cookie sent | (iv) Sandbox holds |
|----------|---------|-------------|--------------------|----------------------|---------------------------------------------|---------------------|--------------------|
| A: `src=/proxy`, panel's current `sandbox="allow-scripts allow-same-origin"` | Chromium | yes | **no** (CSP `sandbox`: "Blocked script execution") | no | not sent: Chromium Local Network Access blocks them ("request client is not a secure context and the resource is in more-private address space `local`") | n/a | yes (origin `null`) |
| A | Firefox | yes | **no** | no | CSS 401, IMG 401 (scripts not requested) | no | yes |
| A | WebKit | yes | **no** | no | CSS 200, IMG 200 | yes | yes |
| B: fetch + `srcdoc`, `sandbox="allow-scripts"` | Chromium | yes | yes | **no** | 401 / 401 / 401 (shown as `ERR_BLOCKED_BY_ORB`) | **no** | yes |
| B | Firefox | yes | yes | **no** | 401 / 401 / 401 (`NS_ERROR_CORRUPTED_CONTENT`) | **no** | yes |
| B | WebKit (Windows) | yes | yes | yes | 200 / 200 / 200 | yes | yes |
| B | WebKit (Linux) | yes | yes | yes | 200 / 200 / 200 | yes | yes |
| C: `blob:` URL, `sandbox="allow-scripts"` | Chromium | yes | yes | **no** | 401 / 401 / 401 | **no** | yes |
| C | Firefox | yes | yes | **no** | 401 / 401 / 401 | **no** | yes |
| C | WebKit (Windows) | yes | yes | yes | 200 / 200 / 200 | yes | yes |
| C | WebKit (Linux) | yes | yes | **no** | IMG 401, CSS/JS cancelled | **no** | yes |

The module script (`type="module"`) fails through `/proxy-resource` in every browser. It is a CORS request
with `Origin: null`. Without a cookie, Grafana returns a 401 with no CORS header. With a cookie (WebKit),
Grafana's CSRF check returns **403 "origin not allowed"**. That check applies to GET too, whenever a valid
session cookie and a foreign `Origin` arrive together (reproduced with curl).

**Isolation (iv), checked from inside the frame in every B and C run, all browsers:**

| Check | Result |
|-------|--------|
| `self.origin` | `"null"` |
| `window.parent.document` | throws `SecurityError` |
| `window.top.location.href` | throws `SecurityError` |
| `document.cookie` | throws `SecurityError` (Chromium, Firefox); returns `""` (WebKit, no Grafana cookie visible) |
| `localStorage` | throws `SecurityError` |
| `fetch(<grafana>/api/user, {credentials:'include'})` | never readable (CORS). The request reaches Grafana as 401 (no cookie) or 403 (WebKit srcdoc: cookie + `Origin: null`, rejected by the CSRF check). |
| Parent reads `iframe.contentDocument` | `null` |

For A, Grafana's `CSP: sandbox` overrides the panel's `allow-same-origin`, so the A document also has origin `null`.

**As-is backend output (no URL fix), all three browsers, A/B/C:** the page renders, but every rewritten
subresource resolves to `http://<upstream>/api/plugins/wilsonwaters-webview-app/resources/proxy-resource?...`.
The upstream nginx logged these requests and returned 404. CSS, external JS and images fail in every browser (defect D1).

## 5. Q2 — other subresource strategies for B and C (13.2.4, `lax`)

Same test page and checks. The results were the same for B and C, and the same on the Windows and Linux builds
unless noted.

| Subresource strategy | Chromium | Firefox | WebKit | Notes |
|----------------------|----------|---------|--------|-------|
| `/proxy-resource` URLs loaded by the frame (section 4) | fail (401) | fail (401) | pass (Linux C: fail) | Depends on cookies |
| **Direct:** absolute upstream URLs, loaded by the browser | **pass** | **pass** | **pass** | CSS, external JS, image and the runtime image all 200. The module script needs upstream CORS for `Origin: null` (nginx sends none, so it fails in all three). |
| **Inline `data:`:** the panel fetches each `/proxy-resource` URL itself (same origin, cookie sent) and replaces it with a `data:` URL | **pass** | **pass** | **pass** | The module script also runs. Runtime-built URLs still go direct. |
| Inline `blob:`: as above, with `URL.createObjectURL` | fail | fail | fail | The opaque-origin frame cannot load the parent's `blob:` URLs |

Isolation held in every variant (same checks as section 4).

**Referrer:** with direct subresources, some requests reached the upstream with `Referer:
http://<grafana-host>:3100/`, because the iframe's `referrerpolicy` covers only the iframe request itself.
Injecting `<meta name="referrer" content="no-referrer">` into the HTML removed the `Referer` in all three browsers.

## 6. Q2 — real pages and Grafana 12.4.13

**Grafana 12.4.13 spot-check (B and C, Windows builds, all three browsers):** identical to 13.2.4.
- `/proxy-resource`: Chromium and Firefox 401, WebKit 200.
- Direct: pass in all three.
- Inline `data:`: pass in all three.
- Isolation holds.

**example.com:** renders in A, B and C in all three browsers (no subresources, no scripts).

**BOM:**
- `https://www.bom.gov.au/products/IDR663.loop.shtml` now returns 301 (via the proxy's redirect handling, two
  hops) to the new site `https://www.bom.gov.au/weather-and-climate/rain-radar-and-weather-maps?radar=66-brisbane-(mt-stapylton)`.
  That page is a React app with Akamai bot protection and reCAPTCHA, and it sends `X-Frame-Options: SAMEORIGIN`.
  In B, only the page shell renders:
  - Its API XHRs get 403 or fail CORS (`Origin: null`), and its web fonts need CORS.
  - Headless Chromium was served bot-block responses (the page has no styles).
  - **The new BOM radar page does not work in proxy mode with any approach.**
- `https://reg.bom.gov.au/products/IDR663.loop.shtml` (legacy loop, 200, 107 KB, 27 subresources) works as follows:
  - B or C, direct subresources, **plus the compatibility shim** (section 10): renders and animates (frames change
    over time) in all three browsers, Windows and Linux.
  - Without the shim, the loop animates only in WebKit. Its script reads `document.cookie`, which throws
    `SecurityError` in a sandboxed document in Chromium and Firefox, and the loop stops.
  - Inline `data:` plus the shim: renders and animates in all three, at 28 proxied requests per load (D3).
  - Through `/proxy-resource`: fails in Chromium and Firefox (401).
  - This page sends **no** `X-Frame-Options` or CSP. **Current direct mode** (`src` = upstream URL) already renders
    and animates it in all three browsers. Proxy mode is not needed for this URL today.

## 7. Q3 — dependence on `cookie_samesite`

This matters only for strategies where the frame loads URLs from Grafana (`/proxy-resource`). The recommended
direct and inline `data:` strategies send no cookie from the frame, so they are independent of this setting.

| `cookie_samesite` | Chromium B/C | Firefox B/C | WebKit (Windows) B/C | A (all browsers) |
|-------------------|--------------|-------------|----------------------|------------------|
| `lax` (default) | 401 | 401 | 200 | scripts blocked; subresources as in section 4 |
| `strict` | 401 | 401 | 200 | same as `lax` |
| `none` (HTTPS, `cookie_secure = true`) | **200** | **403** | 200 | scripts blocked; Chromium Local Network Access permission denied; Firefox CSS fails, IMG 403; WebKit 200 |

Why Firefox gets 403 with `none`: Firefox sends the cookie, and also `Origin: null`, on the frame's subresource
requests. Grafana's CSRF middleware rejects any request that carries a valid session cookie and a foreign
`Origin` ("origin not allowed"). Chromium sends no `Origin` on no-cors GETs.

`none` therefore fixes only Chromium. It also weakens the session cookie for the whole Grafana instance.
**Do not use it as the fix.**

Playwright's Windows WebKit reports the `Lax` cookie as `SameSite=None` and does not enforce SameSite. Its
cookie results do not represent Safari. Linux WebKit (libsoup) enforces SameSite. It sent the `Lax` cookie from
a srcdoc frame (B) but not from a `blob:` frame (C).

## 8. Q4 — `[security] content_security_policy = true` (record only)

Grafana 13.2.4 default template:
`script-src 'self' 'unsafe-eval' 'unsafe-inline' 'strict-dynamic' 'nonce-…'; style-src 'self' 'unsafe-inline' blob:;
img-src * data: blob:; base-uri 'self'; …`.
The srcdoc and `blob:` documents inherit this policy from the dashboard page.

| Strategy | Chromium | Firefox | WebKit |
|----------|----------|---------|--------|
| B/C direct | renders; `<base href>` blocked (`base-uri 'self'`); inline and external scripts blocked; upstream CSS blocked; images load | same | same |
| B/C inline `data:` | renders; `data:` scripts and `data:` CSS blocked; images load | same | same |

So with the Grafana CSP on, proxy mode shows static markup and images only. Not tested: stamping the page's CSP
nonce onto the proxied scripts (`strict-dynamic` would then trust them). That may be a way forward if this
configuration has to be supported.

## 9. Defects found in the current code

| ID | Defect | Evidence | Impact |
|----|--------|----------|--------|
| D1 | `rewrite.go` emits path-absolute `/api/plugins/...` URLs and injects `<base href="<upstream page URL>">`. Path-absolute URLs resolve against `<base>`, so all rewritten subresource and navigation URLs point at the upstream host. The code comment says the base "cannot corrupt them"; that is wrong. `resourceBase` also ignores Grafana's sub-path. | Upstream nginx logs `GET /api/plugins/wilsonwaters-webview-app/resources/proxy-resource?...` → 404 | Proxy-mode subresources fail in every approach and browser today |
| D2 | `Access-Control-Allow-Origin: *` (and `-Methods`, `-Headers`) is sent **twice** on `/proxy` and `/proxy-resource`. `serve()` sets it on the writer, `stripFramingHeaders` sets it on the upstream response, and `ReverseProxy` appends one to the other. | `curl -i` | A duplicated ACAO value is invalid for CORS. The panel's fetch is same-origin, so CORS headers are not needed at all. |
| D3 | Default limits (30 requests/min per domain, 10 concurrent) are below the per-load request count when subresources go through the proxy (BOM loop: 28 per load). | 429 on the next load within a minute | Not an issue with direct subresources (1 request per load) |
| D4 | Navigation refs (`a`, `area`, `iframe`, GET `form`) are rewritten to `/proxy`. In a srcdoc frame, a nested `<iframe src=/proxy…>` hits `X-Frame-Options: deny`, and a followed link loads a CSP-sandboxed document with no scripts. | Section 4 (A) behaviour | Rewriting navigation to `/proxy` has no benefit in the srcdoc design |

## 10. Recommendation (input for FR5)

**Approach B: fetch the rewritten HTML in the panel, render it with `srcdoc`.**

```tsx
// sketch: proxy mode only; direct mode is unchanged
const res = await fetch(`${config.appSubUrl}${PROXY_RESOURCE_BASE}?${params}`, { credentials: 'same-origin' });
// (or getBackendSrv().fetch({ url, responseType: 'text' }))
if (!res.ok) { /* show an error state; do not srcdoc an error body */ }
const html = await res.text();
<iframe title="Web View" srcdoc={html} sandbox="allow-scripts" referrerPolicy="no-referrer" />
```

- **Exact sandbox value: `sandbox="allow-scripts"`.** Never add `allow-same-origin`: a srcdoc frame with it is
  same-origin with Grafana. Do not add `allow-popups`, `allow-forms`, `allow-top-navigation*` or `allow-modals`.
- Prefer B over C. Both behave the same in Chromium and Firefox. B needs no `blob:` URL lifecycle
  (`revokeObjectURL`), and C lost cookies in Linux WebKit.
- `fetch` follows the proxy's redirect chain (tested: BOM, two redirects, final 200). The final page's
  `<base href>` comes from the last hop.
- The panel must not trust `postMessage` from the frame (the frame can post to its parent).
- Auto-refresh refetches the HTML on each tick.

**Backend changes:**
1. **Rewriter (fixes D1, D4):** emit **absolute upstream URLs** for subresources and navigation refs, not
   `/proxy-resource` or `/proxy` paths. Keep `<base href>`: runtime-built URLs and CSS `url()` need it.
   Keep frame-buster removal, CSP-meta removal and hide-selectors as they are.
2. **Inject at the start of `<head>`** (directly after `<base>`), with goquery as for the other rewrites:
   - `<meta name="referrer" content="no-referrer">` (stops the Grafana origin leaking to the upstream);
   - the sandbox compatibility shim below. It gives the page in-memory `document.cookie`, `localStorage` and
     `sessionStorage` only when the real ones throw. It grants no access to Grafana. Without it, the BOM loop
     stops in Chromium and Firefox.
3. **Do not keep `X-Allow-Embedding: allow`** (the experiment on this branch). B never frames the resource
   route, so `X-Frame-Options: deny` stays as defence in depth.
4. **Fix D2:** remove the CORS headers from `/proxy` (same-origin use only), or set them once.
5. **`/proxy-resource`:** the recommended flow does not use it. Keep it only if an optional "load subresources
   through Grafana" mode is added: the panel inlines them as `data:` URLs (tested working in all three browsers),
   which is useful when the browser cannot reach the upstream. That mode needs higher default rate limits (D3)
   and does not cover CSS `url()` or runtime-built URLs. Otherwise remove the route to reduce attack surface.

Shim used in the tests (minified; the BOM loop needs the `document.cookie` part):

```html
<script>(function(){var jar="";try{document.cookie}catch(e){try{Object.defineProperty(Document.prototype,"cookie",{configurable:true,get:function(){return jar},set:function(v){var kv=String(v).split(";")[0];var k=kv.split("=")[0];var parts=jar?jar.split("; ").filter(function(p){return p.split("=")[0]!==k}):[];parts.push(kv);jar=parts.join("; ")}})}catch(e2){}}function mem(){var s={};return{getItem:function(k){return Object.prototype.hasOwnProperty.call(s,k)?s[k]:null},setItem:function(k,v){s[k]=String(v)},removeItem:function(k){delete s[k]},clear:function(){s={}},key:function(i){return Object.keys(s)[i]||null},get length(){return Object.keys(s).length}}}["localStorage","sessionStorage"].forEach(function(n){try{window[n].length}catch(e){try{Object.defineProperty(window,n,{configurable:true,value:mem()})}catch(e2){}}})})();</script>
```

## 11. Limitations users must know (proxy mode, recommended design)

1. **Grafana's own CSP:** with `[security] content_security_policy = true`, proxied pages show markup and images
   only. Scripts and external stylesheets are blocked.
2. **Only the HTML document goes through Grafana.** Images, scripts and stylesheets load from the upstream site
   directly in the viewer's browser. The viewer must be able to reach that site, and the site sees the viewer's
   IP address (no `Referer`).
3. **The page runs in an isolated origin.** Cookies and storage are in memory only (shim). Requests back to its
   own server are cross-origin (`Origin: null`). Single-page apps that call their own APIs do not work, for
   example the new BOM radar page. Module scripts and web fonts work only if the upstream sends
   `Access-Control-Allow-Origin: *`.
4. Sites with bot protection (Akamai, reCAPTCHA) can block the server-side fetch or the browser loads.
5. The new BOM radar URL does not work in proxy mode. The legacy loop
   (`https://reg.bom.gov.au/products/IDR663.loop.shtml`) works in proxy mode, and in direct mode too, because it
   sends no framing headers.

## 12. Q5 — deployment-side configuration

Not required: approach B with direct (or inline `data:`) subresources works on stock Grafana in all three
browsers. For the record, no single Grafana setting made frame-loaded `/proxy-resource` URLs work in all three:
`cookie_samesite = none` fixed Chromium only (section 7). A reverse proxy was not built or tested.

## 13. Not tested, and why

- **Real Safari (macOS/iOS):** no macOS host. Playwright's Windows WebKit does not enforce SameSite, so Linux
  WebKit was the cross-check. The recommended design does not depend on cookies from the frame.
- **Branded Chrome or Edge, Firefox ESR:** Playwright builds only.
- **Grafana on a public address with a private upstream:** Chromium Local Network Access may then prompt or
  block the browser's direct subresource loads (public → local). This is an inference, not tested.
- **`cookie_samesite = none` on plain HTTP** (browsers reject `Secure` cookies there) and `disabled`.
- **Grafana served under a sub-path, Grafana Enterprise or Cloud.**
- **Nonce stamping under the Grafana CSP** (section 8).

## 14. Experiment artefacts

- Experimental change on this branch: `pkg/plugin/proxy.go` sets `X-Allow-Embedding: allow` on `/proxy`
  responses (Go tests pass). Section 10 item 3 recommends reverting it.
- Scripts, screenshots and raw JSON results are outside the repo, in the session scratchpad (`scratchpad/r1/`):
  `r1.mjs` harness, `summarize.mjs`, `probe-api.mjs`, `setup.mjs`, `upstream/` test page. They are not committed.
- All `r1-*` containers and the `r1-net` network were removed after the runs.
