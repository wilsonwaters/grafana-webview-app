# Troubleshooting

Symptom → cause → fix for the **Web View** app plugin
(`wilsonwaters-webview-app`). Each entry is grounded in the plugin's actual
behaviour.

Related: [Installation guide](./installation.md) ·
[Administration guide](./administration.md) · [`docs/signing.md`](./signing.md) ·
[`docs/architecture.md`](./architecture.md) · [README](../README.md).

Before anything else, two facts explain most issues:

1. **The allowlist is empty by default and denies everything (403).** Nothing
   proxies, and **Test URL** fails, until an administrator adds domains on the
   Configuration page. See [Administration guide §2](./administration.md#2-allowlist-management).
2. **Proxy mode sends only the HTML page through Grafana.** The panel shows it in
   an isolated frame, and the viewer's browser loads the page's images, scripts
   and stylesheets directly from the site. See
   [Proxy-mode page shows without styles, images or scripts](#proxy-mode-page-shows-without-styles-images-or-scripts).

---

## Contents

- [Install / load](#install--load)
  - [Plugin won't load — invalid signature / `rootUrls` mismatch](#plugin-wont-load--invalid-signature--rooturls-mismatch)
  - [Plugin won't load — unsigned not allowed](#plugin-wont-load--unsigned-not-allowed)
  - [App loads but the Web View panel is missing](#app-loads-but-the-web-view-panel-is-missing)
  - [Changes to plugin.json / settings don't take effect](#changes-to-pluginjson--settings-dont-take-effect)
- [Panel URL](#panel-url)
  - [Panel shows "This URL cannot be displayed"](#panel-shows-this-url-cannot-be-displayed)
- [Proxy / allowlist](#proxy--allowlist)
  - [Panel or Test URL returns 403 "not allowlisted"](#panel-or-test-url-returns-403-not-allowlisted)
  - [403 even though I configured the domain](#403-even-though-i-configured-the-domain)
  - [Test URL returns 403 "requires the Editor or Admin role"](#test-url-returns-403-requires-the-editor-or-admin-role)
  - [403 for an internal / private-IP target](#403-for-an-internal--private-ip-target)
  - [Proxy-mode page shows without styles, images or scripts](#proxy-mode-page-shows-without-styles-images-or-scripts)
- [Direct mode](#direct-mode)
  - [Direct iframe is blank / page refuses to display](#direct-iframe-is-blank--page-refuses-to-display)
  - [Proxy mode and "Test URL" are greyed out](#proxy-mode-and-test-url-are-greyed-out)
- [Errors under load / bad targets](#errors-under-load--bad-targets)
  - [429 Too Many Requests](#429-too-many-requests)
  - [413 Payload / response too large](#413-payload--response-too-large)
  - [504 Timeout](#504-timeout)
  - [502 Upstream / redirect errors](#502-upstream--redirect-errors)
  - [400 Bad request (scheme / port / url)](#400-bad-request-scheme--port--url)
- [Observability](#observability)
  - [Metrics are not visible](#metrics-are-not-visible)
- [Error-code → reason map](#error-code--reason-map)

---

## Install / load

### Plugin won't load — invalid signature / `rootUrls` mismatch

**Symptom:** Grafana logs a signature error for `wilsonwaters-webview-app` (or the
plugin shows as "invalid signature"); the app does not appear or won't enable.

**Cause:** this is a **privately-signed** build (Path 1), valid **only** for the
Grafana base URL(s) it was signed for (`rootUrls`). If your Grafana's configured
`[server] root_url` does not exactly match one of the signed `rootUrls`, the
signature is rejected. The published release is signed for the project's
deployment host (`https://graphs.alintech.com.au`) **plus**
`http://localhost:3000` only — any other URL mismatches.

**Fix:**

- Make Grafana's `root_url` exactly match a signed URL (scheme + host + any
  sub-path), **or**
- **Re-sign for your instance:** `npx @grafana/sign-plugin@latest --rootUrls https://grafana.example.com`
  (see [`docs/signing.md`](./signing.md#private-signing-for-specific-instances-path-1)),
  **or**
- Run **unsigned** by allow-listing both ids (see next entry). Do this only on a
  trusted network.

A public/community signature is separately rejected with **HTTP 409** until the
plugin is accepted into the Grafana catalog (Path 2, not yet submitted) — do not
try to community-sign this build. See [`docs/signing.md`](./signing.md).

### Plugin won't load — unsigned not allowed

**Symptom:** Grafana refuses to load an **unsigned** build, or the nested panel is
missing while the app loads.

**Cause:** Grafana only loads unsigned plugins that are explicitly allow-listed.
The plugin ships **two** ids — the app `wilsonwaters-webview-app` and the nested
panel `wilsonwaters-webview-panel` — and **both** must be listed.

**Fix:** set (and restart Grafana):

```ini
# grafana.ini — [plugins]
allow_loading_unsigned_plugins = wilsonwaters-webview-app,wilsonwaters-webview-panel
```

or `GF_PLUGINS_ALLOW_LOADING_UNSIGNED_PLUGINS=wilsonwaters-webview-app,wilsonwaters-webview-panel`.
See [Installation guide §2](./installation.md#2-signed-vs-unsigned-and-the-rooturls-requirement).

### App loads but the Web View panel is missing

**Symptom:** the app enables fine, but there is no **Web View** panel type to add
to a dashboard.

**Cause:** the nested panel registers under its own id,
`wilsonwaters-webview-panel`. If you are running unsigned and only allow-listed the
app id, the panel is blocked.

**Fix:** add `wilsonwaters-webview-panel` to `allow_loading_unsigned_plugins`
(see above) and restart Grafana.

### Changes to plugin.json / settings don't take effect

**Symptom:** you edited the allowlist, tuning, or `plugin.json`, but behaviour is
unchanged.

**Cause and fix:**

- **Settings saved on the Configuration page** apply to the next request, with
  no restart. If nothing changed, check that the save succeeded: when a field is
  invalid, the page shows *"The settings were not saved"* and marks the field.
  Correct it and save again.
- **Settings that come back after a restart** are set by a provisioning file.
  Grafana applies `provisioning/plugins/*.yaml` at every start and replaces the
  saved settings. Change or remove the file. See
  [Administration guide §11](./administration.md#11-known-operational-caveats).
- **`plugin.json` changes** are read only when Grafana starts. Restart Grafana.

Then re-verify with a Test URL. See
[Administration guide §2](./administration.md#2-allowlist-management) and
[§10](./administration.md#10-upgrade--rollback).

---

## Panel URL

### Panel shows "This URL cannot be displayed"

**Symptom:** the panel (or the editor preview) shows *"This URL cannot be
displayed"* followed by a reason, and no page.

**Cause:** the panel loads only absolute `http://` or `https://` URLs with a
host. It refuses other schemes (for example `javascript:`, `data:`, `blob:`,
`file:`), relative or protocol-relative URLs, URLs with a user name or password,
URLs that contain control characters such as tabs or line breaks, and URLs over
8192 characters. This check protects every viewer of the dashboard.

**Fix:** enter the full address, for example `https://status.example.com/page`.
Remove any `user:password@` part and any pasted line breaks.

---

## Proxy / allowlist

### Panel or Test URL returns 403 "not allowlisted"

**Symptom:** proxy fetches / "Test URL" return **403**; `webview_proxy_denials_total{reason="allowlist"}`
increments.

**Cause:** the target host is not on the allowlist. The allowlist is **empty by
default and denies all requests** — this is intentional SSRF/open-proxy
containment, not a bug. **Test URL** uses the same allowlist, so it also returns
403 for a site that is not on the list, even one that allows framing.

**Fix:** an administrator adds the host on the app's **Configuration** page
(**Administration → Plugins and data → Plugins → Webview → Configuration**) and
clicks **Save settings**. The change applies to the next request. Note:

- **Domain** is a **bare hostname** (no scheme, port, or path).
- Exact match only unless **Allow subdomains** is selected.
- Only ports 80/443 unless the entry lists **Additional ports**.

See [Administration guide §2](./administration.md#2-allowlist-management).

### 403 even though I configured the domain

**Symptom:** you added the domain, but the host still returns **403** with
*"target host is not allowlisted"*.

**Common causes:**

1. **The host does not match the entry.** For example the page is on
   `www.example.com` but the entry is `example.com` without **Allow
   subdomains**.
2. **A redirect leaves the allowlist.** The page redirects to another host that
   is not on the list (the message then says *"redirect target host is not
   allowlisted"*). Add that host too.
3. **A provisioning file replaced the saved settings.** Grafana applies
   `provisioning/plugins/*.yaml` at every start; it replaces what was saved on
   the Configuration page.
4. **The save did not succeed.** The page shows *"The settings were not saved"*
   when a field is invalid.

**Fix:** open the Configuration page and check the entry, add any redirect
target, and save again. Then run **Test URL** against the host and confirm you
get a verdict rather than a 403. See
[Administration guide §11](./administration.md#11-known-operational-caveats).

### Test URL returns 403 "requires the Editor or Admin role"

**Symptom:** **Test URL** shows *"The URL test requires the Editor or Admin
role."*; `denials_total{reason="role"}` increments.

**Cause:** the check is limited to users whose basic organisation role is
Editor or Admin. A custom RBAC role that lets a Viewer edit dashboards does not
count.

**Fix:** ask an administrator to give you the Editor role, or set the **Load
mode** to **Direct** or **Proxy** by hand. Viewers can still see proxy-mode
panels.

### 403 for an internal / private-IP target

**Symptom:** a target that resolves to a private/internal/metadata address returns
**403**; `denials_total{reason="ip-blocklist"}` or `{reason="metadata"}` increments.

**Cause:** the backend enforces a **hardcoded, non-configurable IP blocklist**
(loopback, RFC 1918 private, link-local **including `169.254.169.254` cloud
metadata**, CGNAT, IPv6 ULA, reserved, and internal IPv4 addresses inside 6to4 or
NAT64 IPv6 addresses) on every resolved and connect-time IP, and blocks
cloud-metadata hostnames by name. This cannot be disabled at runtime.

**Fix:**

- For a **trusted internal RFC 1918 target** only, an admin may select the
  per-domain **Allow private IP addresses** opt-in (`allowPrivateIP`) — this
  relaxes **RFC 1918 IPv4 only** and never loopback, link-local, or the metadata
  IP. Remember that each viewer's browser must also reach the site to load its
  images, scripts and stylesheets. See
  [Administration guide §3](./administration.md#3-the-allowprivateip-opt-in).
- Cloud-metadata endpoints are **never** reachable; there is no opt-in for them.
- If you also run Grafana `[security.egress]` controls, the target must pass those
  too — see [Administration guide §9](./administration.md#9-interaction-with-grafanas-securityegress-controls).

### Proxy-mode page shows without styles, images or scripts

**Symptom:** a proxy-mode panel shows the page text, but styles, images or
interactive parts are missing; or the page loads but stays empty or shows its
own error.

Proxy mode sends only the HTML page through Grafana. The panel shows it in a
frame with an isolated origin (`sandbox="allow-scripts"`), and the viewer's
browser loads images, scripts and stylesheets directly from the site. No
Grafana setting such as `allow_embedding` is needed. Check these causes in
order:

| What you see | Likely cause | Fix |
| --- | --- | --- |
| Markup and images only; no scripts or external styles, in every browser | Grafana runs with `[security] content_security_policy = true`. The proxied page inherits Grafana's CSP. | Use direct mode for that site if it allows framing, or turn the Grafana CSP off if your security policy allows it. |
| No styles and no images | The viewer's browser cannot reach the site (for example an internal host that only the Grafana server can reach). | Make the site reachable from viewers' browsers, or use a different page. |
| Page shell renders but its data never appears | The page is a single-page app that calls its own APIs. From the isolated frame these calls are cross-origin and fail. | Not supported in proxy mode. Use direct mode if the site allows framing. |
| Page asks for a login, forgets settings, or shows a consent banner on every load | Cookies and storage are in memory only and reset on every reload; the proxy sends no credentials. | Not supported. Use a public page. A banner that is part of the page HTML can be hidden with `hideSelectors`. |
| Text in a fallback font, or a module script fails | Web fonts and module scripts need the site to allow cross-origin requests. | Usually cosmetic. Not fixable from Grafana. |
| A "blocked" or captcha page, or 403/429 from the site | Bot protection (for example Akamai or reCAPTCHA) blocks the proxy or the browser. | Not supported. Use direct mode if the site allows framing. |

The browser's developer tools (Console and Network tabs) show which requests
fail. For example, the new Bureau of Meteorology radar page is a single-page app
with bot protection and does not work in proxy mode, but the legacy loop
`https://reg.bom.gov.au/products/IDR663.loop.shtml` works in both modes.

If instead the panel shows *"The page could not be loaded through the backend
proxy (HTTP …)"*, the proxy refused the request: look up the status in the
[error-code map](#error-code--reason-map). If it shows that the backend proxy is
unavailable, see
["Proxy mode greyed out"](#proxy-mode-and-test-url-are-greyed-out).

See [`docs/architecture.md` §7](./architecture.md#7-proxy-mode-rendering-fr5)
for the design.

---

## Direct mode

### Direct iframe is blank / page refuses to display

**Symptom:** in **Direct** mode the panel is blank; the browser console shows an
`X-Frame-Options` / CSP `frame-ancestors` refusal.

**Cause:** the target site forbids being framed. Direct mode embeds the URL
straight into an iframe with no server-side involvement, so the browser enforces
the site's framing headers.

**Fix:** the page needs **proxy mode** (the backend strips `X-Frame-Options` and
the CSP `frame-ancestors` directive). Ask an administrator to allowlist the
domain, then run "Test URL" — it recommends `proxy` for framing-hostile pages —
or set **Load mode** to **Proxy**. Read
[the proxy-mode entry above](#proxy-mode-page-shows-without-styles-images-or-scripts)
for the kinds of page that do not work in proxy mode.

### Proxy mode and "Test URL" are greyed out

**Symptom:** the editor offers only Direct mode; Proxy and "Test URL" are disabled.

**Cause:** **no backend was detected.** The frontend probes the `/health` resource
and, when the backend is absent (e.g. a Grafana deployment without backend-plugin
support), degrades to **direct-iframe-only** mode by design.

**Fix:** install on a **backend-capable** Grafana host and confirm the backend
binary is present and executable. See
[Installation guide §1](./installation.md#1-prerequisites). If the backend should
be present, check the Grafana server logs for a plugin-backend start failure
(wrong architecture, non-executable binary, signature rejection).

---

## Errors under load / bad targets

All limits below are set in the **Global limits** section of the Configuration
page (stored in `jsonData`) and are **fail-closed** (a zero/negative value is
reset to the default — you loosen a limit by *raising* it, never by zeroing it).
See [Administration guide §4](./administration.md#4-tuning).

### 429 Too Many Requests

**Symptom:** proxy requests return **429**; `denials_total{reason="rate-limit"}` or
`{reason="concurrency"}` increments.

**Cause:** a rate tier or the concurrency cap was exceeded — the per-organisation
tier (**Rate limit per Grafana instance**, `rateLimitPerInstancePerMin`,
default 60), the per-domain tier (`rateLimitPerDomainPerMin`, default 30), or
`maxConcurrentRequests` (default 10). Every proxy-mode panel on every open
dashboard makes one request per load and per auto-refresh, and **Test URL**
counts too.

**Fix:** raise the relevant limit for high-traffic dashboards, or lengthen the
panels' auto-refresh interval; prefer a per-domain **Rate limit override** when
only one busy domain needs headroom. If the spike is unexpected, check the audit
log / metrics for abuse before raising limits. See
[Administration guide §4](./administration.md#4-tuning).

### 413 Payload / response too large

**Symptom:** proxy returns **413**; `denials_total{reason="size-limit"}` increments.

**Cause:** the upstream response body exceeds `maxResponseBytes` (default 5 MiB).
This also bounds the **gzip-decoded** size (decompression-bomb guard) and the
size of the rewritten HTML, so a small compressed body can still trip it if it
decodes large. The proxy reads the whole response before it replies, so an
oversize response is always a 413, never a cut-off page.

**Fix:** raise `maxResponseBytes` only if you legitimately proxy large pages. See
[Administration guide §4](./administration.md#4-tuning).

### 504 Timeout

**Symptom:** proxy returns **504**; `denials_total{reason="timeout"}` increments.

**Cause:** the per-request budget (`requestTimeoutSec`, default 10s) expired. This
one budget covers the **whole** round-trip and body copy, so a slow/stalled
upstream body trips it too.

**Fix:** raise `requestTimeoutSec` for slow upstreams, but keep it low enough that
stalled fetches don't pile up against `maxConcurrentRequests`. See
[Administration guide §4](./administration.md#4-tuning).

### 502 Upstream / redirect errors

**Symptom:** proxy returns **502**; `denials_total{reason="upstream"}` or
`{reason="redirect-loop"}` increments.

**Cause:** an upstream resolve/transport/gateway failure, a response with a
`Content-Encoding` other than gzip (the proxy cannot bound its decoded size), a
malformed gzip body, **or** the redirect depth cap (`maxRedirects`, default 3)
was exceeded. Each redirect hop is re-validated against the allowlist and IP
blocklist; a redirect **into a non-allowlisted host** is a 403 (reason
`allowlist`), not a 502.

**Fix:** confirm the upstream is reachable from the Grafana host (and permitted by
`[security.egress]` if you run it). Raise `maxRedirects` if the target legitimately
chains redirects; `0` disables redirect following entirely. See
[Administration guide §4](./administration.md#4-tuning).

### 400 Bad request (scheme / port / url)

**Symptom:** **400** with `denials_total{reason="scheme"}` or `{reason="bad-request"}`.

**Cause:** the URL is missing/unbuildable, uses a non-`http(s)` scheme, embeds
userinfo (`user:pass@host`), or targets a disallowed port (only 80/443 unless the
matched domain lists extra `allowedPorts`). A non-GET method returns **405**
(`reason="method"`).

**Fix:** supply a valid, percent-encoded absolute `http`/`https` URL with no
embedded credentials; add the required port to the domain's `allowedPorts` if it
is non-standard. See [Administration guide §2](./administration.md#2-allowlist-management).

---

## Observability

### Metrics are not visible

**Symptom:** the `webview_proxy_*` Prometheus metrics don't show up.

**Cause:** the metrics are **not** a plugin resource route. The collectors are
registered on Prometheus's default registry and served on the **plugin backend's
standard metrics endpoint** via the Grafana plugin SDK diagnostics adapter.

**Fix:** scrape the plugin backend's metrics the same way as any plugin backend's
metrics, and confirm the backend is actually running (if it isn't, see
["Proxy mode greyed out"](#proxy-mode-and-test-url-are-greyed-out) — no backend
means no metrics). The audit log (one `proxy request` line per request) is in the
Grafana server log output. See
[Administration guide §5–6](./administration.md#5-audit-logging).

---

## Error-code → reason map

Every denial's HTTP status **and** metric `reason` come from the single
authoritative `reasonStatus` table (`pkg/plugin/metrics.go`), so the status and
the recorded reason never drift. Full details:
[Administration guide §6](./administration.md#6-metrics--suggested-alerts).

| Status | `denials_total{reason}` | Cause | Where to look |
| --- | --- | --- | --- |
| **403** | `allowlist` | Host not allowlisted (incl. redirect into a non-allowlisted host) | [Allowlist 403](#panel-or-test-url-returns-403-not-allowlisted), [Configured but 403](#403-even-though-i-configured-the-domain) |
| **403** | `role` | **Test URL** by a user whose org role is below Editor | [Role 403](#test-url-returns-403-requires-the-editor-or-admin-role) |
| **403** | `ip-blocklist` | Resolved/connect IP is blocked | [Private-IP 403](#403-for-an-internal--private-ip-target) |
| **403** | `metadata` | Cloud-metadata host | [Private-IP 403](#403-for-an-internal--private-ip-target) |
| **429** | `rate-limit` | Per-instance or per-domain rate tier exceeded | [429](#429-too-many-requests) |
| **429** | `concurrency` | Concurrency cap exhausted | [429](#429-too-many-requests) |
| **413** | `size-limit` | Response exceeds `maxResponseBytes` | [413](#413-payload--response-too-large) |
| **400** | `scheme` | Bad scheme / port / userinfo / host | [400](#400-bad-request-scheme--port--url) |
| **400** | `bad-request` | Missing `url` / unbuildable target | [400](#400-bad-request-scheme--port--url) |
| **405** | `method` | Non-GET method | [400](#400-bad-request-scheme--port--url) |
| **504** | `timeout` | Per-request budget expired | [504](#504-timeout) |
| **502** | `upstream` | Upstream resolve/transport/gateway failure, unsupported `Content-Encoding` | [502](#502-upstream--redirect-errors) |
| **502** | `redirect-loop` | Redirect depth cap exceeded | [502](#502-upstream--redirect-errors) |

If you believe a denial indicates a **security bypass** (e.g. an internal address
was reached), do not open a public issue — follow [`SECURITY.md`](../SECURITY.md).
