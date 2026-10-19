# Runtime verification: integrated v1.0.0 build

**Date:** 2026-10-19. **Build:** `release/v1.0.0` @ `8e46750` (S1 + FR5 frontend + FR5 backend + S2 + AU1).
**Result:** 9 of 9 scenarios pass in all 6 browser × Grafana combinations. No product defect found.

## Setup

| Item | Value |
|------|-------|
| Grafana | `grafana/grafana:13.2.4` (port 3500) and `grafana/grafana:12.4.13` (port 3501). Plain `docker run`, `dist/` mounted as the plugin directory, unsigned loading allowed for `wilsonwaters-webview-app,wilsonwaters-webview-panel`, `GF_AUTH_ANONYMOUS_ENABLED=false`. No provisioning: the app starts disabled with an empty allowlist. |
| Build | Frontend `npm ci && npm run build` in `node:22`; backend `mage -v build:linux`. |
| Users | `admin` (Admin), `editor` (Editor) and `viewer` (Viewer), created with `POST /api/admin/users` and `PATCH /api/org/users/:id`. Each logs in through the Grafana login form. |
| Browsers | Playwright 1.64.1 on Windows: Chromium 156.0.8078.4, Firefox 157.0, WebKit 27.2. Viewport 1440 × 1000. |
| Grafana address | `http://localhost:<port>`. |
| Accessibility | `@axe-core/playwright` (all rules), full page. A finding counts against the plugin only when the node is inside a plugin root (`webview-panel-container`, `ac-settings-form`) and is not inside the framed third-party page. |

Before each browser run the harness reset the app (disabled, `allowedDomains: []`), so every browser did the full admin flow.
The admin enabled the app with the plugin page's **Enable** button. The editor built the dashboard in the UI (new
dashboard → add visualization → Web View → Save). A second dashboard ("checks") with six panels for S5–S8 was created
through the HTTP API as test setup.

## Results

| Scenario | 13.2.4 Chromium | 13.2.4 Firefox | 13.2.4 WebKit | 12.4.13 Chromium | 12.4.13 Firefox | 12.4.13 WebKit |
|---|---|---|---|---|---|---|
| S1 Admin sets the allowlist in the Configuration page; persists after reload | pass | pass | pass | pass | pass | pass |
| S2 Editor builds a proxy-mode BOM panel; Test URL works; saves | pass | pass | pass | pass | pass | pass |
| S3 Viewer: BOM radar renders and animates; frame isolated | pass | pass | pass | pass | pass | pass |
| S4 Viewer: `check-frameable` 403, `proxy` 200 | pass | pass | pass | pass | pass | pass |
| S5 Direct mode renders; `javascript:` URL shows the error state | pass | pass | pass | pass | pass | pass |
| S6 Proxy, host not in the allowlist: accessible 403 error state | pass | pass | pass | pass | pass | pass |
| S7 Auto-refresh refetches; no plugin console errors | pass | pass | pass | pass | pass | pass |
| S8 Pan and zoom apply in proxy mode | pass | pass | pass | pass | pass | pass |
| S9 Dark and light theme (panel and Configuration page) | pass | pass | pass | pass | pass | pass |
| axe: plugin critical/serious (BOM dashboard / checks dashboard / Configuration page) | 0/0/0 | 0/0/0 | 0/0/0 | 0/0/0 | 0/0/0 | 0/0/0 |

## Evidence per scenario (same in all 6 runs unless stated)

- **S1.** The page opened with 0 entries and the "allowlist is empty" alert. The admin added `example.com` and
  `reg.bom.gov.au` with **Add domain**, clicked **Save settings** (the page reloads), then reloaded again. Both entries
  are shown, `GET /api/plugins/wilsonwaters-webview-app/settings` returns both in `jsonData.allowedDomains`, and
  `enabled` is `true`.
- **S2.** The editor first typed `javascript:alert(1)`: the panel showed "This URL cannot be displayed", no iframe in the
  panel or the viewport preview, no dialog. With the BOM URL, **Test URL** called `check-frameable` → HTTP 200, result
  "Direct — frames without a proxy / no framing restrictions" (the legacy loop page sends no framing headers). After
  **Proxy** was selected, the panel showed a `srcdoc` frame with `sandbox="allow-scripts"`. The dashboard was saved.
- **S3.** In the viewer's dashboard the panel iframe has `sandbox="allow-scripts"`, no `src`, a `srcdoc` of about
  94 KB and `referrerpolicy="no-referrer"`. In the frame, `self.origin` is `"null"` and `window.parent.document` and
  `window.top.location.href` throw. Over 12 s the `#animation` image showed 7 different radar frames
  (`IDR663.T.<time>.png`) and was painted. All 7 radar PNG requests went to `https://reg.bom.gov.au/radar/` and returned
  200. The only plugin call was one `GET …/resources/proxy` → 200; there was no 401 or 404 on `/api/plugins/...`.
- **S4.** As the viewer, `fetch` of `…/resources/check-frameable?url=https://example.com` → **403**
  `{"error":"Forbidden","reason":"role","message":"The URL test requires the Editor or Admin role."}`;
  `…/resources/proxy?url=https://example.com/` → **200** with the "Example Domain" page.
- **S5.** Direct panel: `sandbox="allow-scripts allow-same-origin"`, `src=https://example.com`, framed title
  "Example Domain". `javascript:alert(1)` in direct mode and in proxy mode: error text with `role="alert"`, 0 iframes,
  0 dialogs.
- **S6.** Proxy panel for `https://www.iana.org/help/example-domains`: `role="alert"` text "The page could not be loaded
  through the backend proxy (HTTP 403). target host is not allowlisted", no iframe.
- **S7.** Proxy panel for `https://example.com/` with `refreshIntervalSec: 3`: 4 refetches in 13 s, all 200. The iframe
  element was replaced, and it kept `sandbox="allow-scripts"`. No plugin console errors in the whole run (admin, editor
  and viewer pages).
- **S8.** In the panel editor, zoom 1 gives `scale(1) translate(0px, 0px)`. The harness measured the radar image in the
  proxied frame (about 483, 325; 512 × 512), then typed X, Y and zoom into the viewport inputs. The panel iframe
  transform became, for example, `scale(0.72) translate(-473px, -314px)` and the preview shows the radar. The viewer's
  zoom-0.5 proxy panel has `scale(0.5) translate(-450px, -300px)`.
- **S9.** With the user preference set to dark, then light, the panel (view-panel mode) and the Configuration page
  render in both themes (body background `rgb(17, 18, 23)` / `rgb(251, 251, 251)`).
- **axe.** Grafana chrome and plugin elements: 0 critical or serious findings on all three pages. The only findings on the
  dashboards (`label` × 8, `color-contrast` × 1) are inside the framed BOM page (its own form inputs). The
  Configuration page has no critical or serious finding.
- **Grafana audit log (all runs on 13.2.4; 12.4.13 is the same except 124 viewer 200s).** `proxy request` lines:
  check-frameable editor 200 × 5, viewer 403 × 5; proxy editor 200 × 20, viewer 200 × 128, viewer 403 × 20 (the S6
  host). Every line names the Grafana user. No `level=error` or `level=warn` lines from the plugin logger on either
  instance.

## Console messages that are not plugin errors

| Message | Source | Why it is not a plugin defect |
|---------|--------|-------------------------------|
| `404` on `/api/gnet/plugins/wilsonwaters-webview-app[/versions/0.0.1[/insights]]` | Grafana plugin details page | Grafana looks up the unpublished plugin in the grafana.com catalog. |
| `XMLHttpRequest … marketing.json … from origin 'null' blocked by CORS` (Chromium, Firefox); `Origin null is not allowed … /akam/13/pixel_…` (WebKit) | Scripts in the framed BOM page and its nested `includes/marketing.html` frame | The proxied page runs in an opaque origin, so requests back to its own server are cross-origin. This is the documented proxy-mode limitation (R1 §11 item 3). The radar loop is not affected. |
| `Failed to initialize OpenFeature provider` (12.4.13, Firefox); `Fetch API cannot load …/public/build/img/icons/unicons/*.svg` (12.4.13, WebKit) | Grafana frontend | Grafana itself (WebKit on Windows also draws no toolbar icons). |
| `403` on `…/check-frameable` (viewer) and on `…/proxy?url=https://www.iana.org/…` | S4 and S6 | Deliberate denials under test. |

## Harness notes (no product impact)

- `POST /api/plugins/<id>/settings` with `"jsonData": {}` leaves the stored `jsonData` unchanged (Grafana skips an
  empty map); a non-empty `jsonData` replaces it. The reset therefore sends `{"allowedDomains": []}`. The settings page
  always sends `allowedDomains`, so it is not affected (checked: clearing a limit field removes the key as intended).
- Firefox and WebKit serialise `translate(0px, 0px)` as `translate(0px)`; the S8 check normalises it.
- Grafana 12.4.13 with WebKit once left an overlay over the visualization picker toggle; the harness now waits for the
  picker before it uses the toggle. That run was repeated and passed.
- In the first full pass, five runs stopped at S1 because the reset above did not clear the allowlist, and in the
  second pass the Firefox S8 check failed only on the serialisation above. Every run in the table is a complete run
  with the corrected harness.

## Limits of this verification

- Playwright browser builds on Windows only. Not tested: branded Chrome or Edge, Safari on macOS or iOS.
- Grafana runs on `localhost` over HTTP, with the default `cookie_samesite` and without `[security]
  content_security_policy`.
- The BOM page is live. The radar loop and its timestamps change between runs.

## Screenshots (`docs/screenshots/v1-runtime/`)

Names are `<grafana>-<browser>-<scenario>.png`.

| Scenario | Files |
|----------|-------|
| S1, S9 | `*-chromium-s9-config-dark.png`, `*-chromium-s9-config-light.png` (both versions; full page, two saved entries) |
| S2 | `13.2.4-chromium-s2-editor-test-url.png`, `13.2.4-chromium-s2-editor-proxy.png`, `12.4.13-chromium-s2-editor-proxy.png` |
| S3 | `*-s3-viewer-viewpanel.png` for all 6 combinations, `12.4.13-chromium-s3-viewer-dashboard.png` |
| S5 | `13.2.4-chromium-s5-editor-javascript-url.png` |
| S5–S8 | `*-chromium-s5-s8-viewer-checks-dashboard.png` (dark), `12.4.13-chromium-s9-checks-light.png` (light) |
| S8 | `*-chromium-s8-editor-zoom-1.00.png`, `13.2.4-chromium-s8-editor-zoom-0.72.png`, `12.4.13-chromium-s8-editor-zoom-0.68.png`, `13.2.4-chromium-s8-viewer-zoom-0.50.png` |
| S9 | `*-chromium-s9-panel-dark.png`, `*-chromium-s9-panel-light.png` |

The harness (`rtv.mjs`), the raw JSON per run and the full screenshot set are in the session scratchpad
(`scratchpad/rtv/`) and are not committed. All `rtv-*` containers were removed after the runs.

> Committed screenshots are a curated subset (6 of 27): 13.2.4-chromium-s3-viewer-viewpanel.png 13.2.4-firefox-s3-viewer-viewpanel.png 13.2.4-chromium-s2-editor-proxy.png 13.2.4-chromium-s5-editor-javascript-url.png 13.2.4-chromium-s9-config-dark.png 12.4.13-chromium-s3-viewer-dashboard.png. The full set and raw results were kept outside the repository.
