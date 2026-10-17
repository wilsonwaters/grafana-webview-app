# Troubleshooting

Symptom → cause → fix for the **Web View** app plugin
(`wilsonwaters-webview-app`). Each entry is grounded in the plugin's actual
behaviour.

Related: [Installation guide](./installation.md) ·
[Administration guide](./administration.md) · [`docs/signing.md`](./signing.md) ·
[`docs/architecture.md`](./architecture.md) · [README](../README.md).

Before anything else, two facts explain most issues:

1. **The allowlist is empty by default and denies everything (403).** Nothing
   proxies until you add domains. See [Administration guide §2](./administration.md#2-allowlist-management).
2. **In-panel proxy rendering is deferred for v1 (FR5 / #102).** The proxy pipeline
   works, but proxied content does not render *inside the panel* yet. See
   [that entry](#in-panel-proxy-mode-content-does-not-render-fr5--102) below.

---

## Contents

- [Install / load](#install--load)
  - [Plugin won't load — invalid signature / `rootUrls` mismatch](#plugin-wont-load--invalid-signature--rooturls-mismatch)
  - [Plugin won't load — unsigned not allowed](#plugin-wont-load--unsigned-not-allowed)
  - [App loads but the Web View panel is missing](#app-loads-but-the-web-view-panel-is-missing)
  - [Changes to plugin.json / settings don't take effect](#changes-to-pluginjson--settings-dont-take-effect)
- [Proxy / allowlist](#proxy--allowlist)
  - [Panel or Test URL returns 403 "not allowlisted"](#panel-or-test-url-returns-403-not-allowlisted)
  - [403 even though I configured the domain — the #113 caveat](#403-even-though-i-configured-the-domain--the-113-caveat)
  - [403 for an internal / private-IP target](#403-for-an-internal--private-ip-target)
  - [In-panel proxy-mode content does not render (FR5 / #102)](#in-panel-proxy-mode-content-does-not-render-fr5--102)
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
signature is rejected. The published release is signed for the deployment host
(`GRAFANA_INSTANCE_URL`) **plus** `http://localhost:3000` only — any other URL
mismatches.

**Fix:**

- Make Grafana's `root_url` exactly match a signed URL (scheme + host + any
  sub-path), **or**
- **Re-sign for your instance:** `npm run sign -- --rootUrls https://grafana.example.com`
  (see [`docs/signing.md`](./signing.md#private-signing-for-specific-instances-path-1)),
  **or**
- Run **unsigned** by allow-listing both ids (see next entry).

A public/community signature is separately rejected with **HTTP 409** until the
plugin is accepted into the Grafana catalog (Path 2, deferred) — do not try to
community-sign this build. See [`docs/signing.md`](./signing.md).

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

**Cause:** the backend parses its settings **once, at instance start**
(`LoadSettings` in `pkg/plugin/config.go`), and `plugin.json` is read at Grafana
startup. Nothing re-reads them live.

**Fix:** **restart Grafana** (or reload the plugin instance) after any settings or
`plugin.json` change, then re-verify with a Test URL. See
[Administration guide §2](./administration.md#2-allowlist-management) and
[§10](./administration.md#10-upgrade--rollback).

---

## Proxy / allowlist

### Panel or Test URL returns 403 "not allowlisted"

**Symptom:** proxy fetches / "Test URL" return **403**; `webview_proxy_denials_total{reason="allowlist"}`
increments.

**Cause:** the target host is not in `jsonData.allowedDomains`. The allowlist is
**empty by default and denies all requests** — this is intentional
SSRF/open-proxy containment, not a bug.

**Fix:** add the host to the allowlist via the plugin-settings API
(`POST /api/plugins/wilsonwaters-webview-app/settings` with the **complete**
`jsonData`). Note:

- `domain` is a **bare hostname** (no scheme, port, or path).
- Exact match only unless you set `allowSubdomains: true`.
- Only ports 80/443 unless you list extra `allowedPorts`.

See [Administration guide §2](./administration.md#2-allowlist-management). Then
restart/reload and re-test. **Do not** use the bundled Configuration page to edit
the allowlist — it overwrites `jsonData` and drops `allowedDomains`.

### 403 even though I configured the domain — the #113 caveat

**Symptom:** you added the domain, but an allowlisted host still returns **403**.

**Cause (two common ones):**

1. **The configuration never reached the backend (#113).** Provisioning
   `jsonData.allowedDomains` via `provisioning/plugins/*.yaml` does **not** reliably
   deliver the allowlist to the backend in every Grafana setup. A 403 on a host you
   believe you allowlisted is the signature of this caveat.
2. **The Configuration page clobbered it.** Saving the bundled Configuration page
   rewrites `jsonData` to just `apiUrl`/`apiKey`, silently dropping
   `allowedDomains`.

**Fix:**

- Configure the allowlist through the **settings API**, not provisioning or the
  Config page (see [Administration guide §2](./administration.md#2-allowlist-management)).
- **Always verify:** run a **Test URL** against an allowlisted host (or
  `/check-frameable?url=…`) and confirm an **HTTP 200** verdict rather than a 403.
  This is the single most reliable confirmation the config reached the backend.
- Restart/reload after changing settings.

See [Administration guide §11](./administration.md#11-known-operational-caveats).

### 403 for an internal / private-IP target

**Symptom:** a target that resolves to a private/internal/metadata address returns
**403**; `denials_total{reason="ip-blocklist"}` or `{reason="metadata"}` increments.

**Cause:** the backend enforces a **hardcoded, non-configurable IP blocklist**
(loopback, RFC 1918 private, link-local **including `169.254.169.254` cloud
metadata**, CGNAT, IPv6 ULA, reserved) on every resolved and connect-time IP, and
blocks cloud-metadata hostnames by name. This cannot be disabled at runtime.

**Fix:**

- For a **trusted internal RFC 1918 target** only, an admin may set the per-domain
  **`allowPrivateIP: true`** opt-in — this relaxes **RFC 1918 IPv4 only** and never
  loopback, link-local, or the metadata IP. See
  [Administration guide §3](./administration.md#3-the-allowprivateip-opt-in).
- Cloud-metadata endpoints are **never** reachable; there is no opt-in for them.
- If you also run Grafana `[security.egress]` controls, the target must pass those
  too — see [Administration guide §9](./administration.md#9-interaction-with-grafanas-securityegress-controls).

### In-panel proxy-mode content does not render (FR5 / #102)

**Symptom:** a page that needs proxy mode (it blocks direct framing) fetches and
sanitises correctly server-side, but the **panel stays blank** in proxy mode — even
though the backend and "Test URL" work.

**Cause:** this is a **known, documented v1 limitation**, not a misconfiguration.
Grafana serves plugin resource routes (`/api/plugins/*/resources/*`) with
`X-Frame-Options: deny` and a sandbox CSP, which forbids the panel iframe from
framing the `/proxy` URL directly (even same-origin). The proxy pipeline itself is
complete and security-tested; only the **in-panel display** of proxied content is
deferred.

**Fix / workaround for v1:**

- **Direct mode is unaffected** — use it for sites that permit framing.
- Do not rely on in-panel proxy rendering in v1. The backend endpoints remain fully
  usable, and the planned fix (fetch-then-`srcdoc`) is deferred post-v1.

See [`docs/architecture.md` §7](./architecture.md#7-known-limitation-in-panel-proxy-rendering-fr5--102),
[Administration guide §11](./administration.md#11-known-operational-caveats), and
[#102](https://github.com/wilsonwaters/grafana-webview-app/issues/102).

---

## Direct mode

### Direct iframe is blank / page refuses to display

**Symptom:** in **Direct** mode the panel is blank; the browser console shows an
`X-Frame-Options` / CSP `frame-ancestors` refusal.

**Cause:** the target site forbids being framed. Direct mode embeds the URL
straight into an iframe with no server-side involvement, so the browser enforces
the site's framing headers.

**Fix:** the page needs **proxy mode** (the backend strips `X-Frame-Options` and
the CSP `frame-ancestors` directive). Run "Test URL" — it recommends `proxy` for
framing-hostile pages. **But note the FR5/#102 limitation above:** in v1, proxy
content does not render in-panel. Direct mode only works for sites that permit
framing. Also confirm the panel's sandbox/network isn't otherwise blocking the
load.

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

All limits below live in `jsonData` and are **fail-closed** (a zero/negative value
is reset to the default — you loosen a limit by *raising* it, never by zeroing it).
See [Administration guide §4](./administration.md#4-tuning).

### 429 Too Many Requests

**Symptom:** proxy requests return **429**; `denials_total{reason="rate-limit"}` or
`{reason="concurrency"}` increments.

**Cause:** a rate tier or the concurrency cap was exceeded — per-instance
(`rateLimitPerInstancePerMin`, default 60), per-domain
(`rateLimitPerDomainPerMin`, default 30), or `maxConcurrentRequests` (default 10).

**Fix:** raise the relevant limit for high-traffic dashboards; prefer a per-domain
`options.rateLimitPerMin` override when only one busy domain needs headroom. If the
spike is unexpected, check the audit log / metrics for abuse before raising limits.
See [Administration guide §4](./administration.md#4-tuning).

### 413 Payload / response too large

**Symptom:** proxy returns **413**; `denials_total{reason="size-limit"}` increments.

**Cause:** the upstream response body exceeds `maxResponseBytes` (default 5 MiB).
This also bounds the **gzip-decoded** size (decompression-bomb guard), so a small
compressed body can still trip it if it decodes large.

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

**Cause:** an upstream resolve/transport/gateway failure, **or** the redirect
depth cap (`maxRedirects`, default 3) was exceeded. Each redirect hop is
re-validated against the allowlist and IP blocklist; a redirect **into a
non-allowlisted host** is a 403 (reason `allowlist`), not a 502.

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
| **403** | `allowlist` | Host not allowlisted (incl. redirect into a non-allowlisted host) | [Allowlist 403](#panel-or-test-url-returns-403-not-allowlisted), [#113 caveat](#403-even-though-i-configured-the-domain--the-113-caveat) |
| **403** | `ip-blocklist` | Resolved/connect IP is blocked | [Private-IP 403](#403-for-an-internal--private-ip-target) |
| **403** | `metadata` | Cloud-metadata host | [Private-IP 403](#403-for-an-internal--private-ip-target) |
| **429** | `rate-limit` | Per-instance or per-domain rate tier exceeded | [429](#429-too-many-requests) |
| **429** | `concurrency` | Concurrency cap exhausted | [429](#429-too-many-requests) |
| **413** | `size-limit` | Response exceeds `maxResponseBytes` | [413](#413-payload--response-too-large) |
| **400** | `scheme` | Bad scheme / port / userinfo / host | [400](#400-bad-request-scheme--port--url) |
| **400** | `bad-request` | Missing `url` / unbuildable target | [400](#400-bad-request-scheme--port--url) |
| **405** | `method` | Non-GET method | [400](#400-bad-request-scheme--port--url) |
| **504** | `timeout` | Per-request budget expired | [504](#504-timeout) |
| **502** | `upstream` | Upstream resolve/transport/gateway failure | [502](#502-upstream--redirect-errors) |
| **502** | `redirect-loop` | Redirect depth cap exceeded | [502](#502-upstream--redirect-errors) |

If you believe a denial indicates a **security bypass** (e.g. an internal address
was reached), do not open a public issue — follow [`SECURITY.md`](../SECURITY.md).
