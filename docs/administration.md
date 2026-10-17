# Administration Guide

Operational guide for Grafana administrators and operators running the **Web View**
app plugin (`wilsonwaters-webview-app`).

The plugin ships a backend proxy that fetches external pages on behalf of
dashboard viewers, strips framing headers, and rewrites HTML so the page renders
inside a panel. Because the backend can reach whatever the Grafana host can reach,
operating it safely is an administrative responsibility. This document is the
operational **how**: setup, configuration, tuning, monitoring, and incident
response.

For the security **why** — the threat model, the layered controls (SF1–SF5), and
the residual-risk analysis — see
[`docs/architecture.md`](./architecture.md). This guide deliberately does **not**
duplicate the threat model; it references it. See also the top-level
[`SECURITY.md`](../SECURITY.md) for the security posture summary and vulnerability
reporting, [`docs/installation.md`](./installation.md) for install methods
(forthcoming), and [`docs/signing.md`](./signing.md) for signing and secrets.

---

## 1. First-time setup checklist

1. **Install the plugin** and restart Grafana. Because this is a backend app
   plugin, Grafana loads it at startup. See
   [`docs/installation.md`](./installation.md) (forthcoming) for install methods
   and [`docs/signing.md`](./signing.md) for signed-build requirements.
2. **Enable the app.** Go to **Administration → Plugins and data → Plugins → Web
   View** and enable it.
3. **Understand the fail-closed default: nothing works until you allowlist
   domains.** On a fresh install the allowlist (`jsonData.allowedDomains`) is
   **empty**, and an empty allowlist **denies every proxy request** (HTTP 403).
   This is intentional containment, not a bug — the plugin proxies nothing until
   you explicitly add the domains you trust.
4. **Add your allowed domains** (see [§2](#2-allowlist-management)).
5. **Verify.** Use the panel's **Test URL** control (or call
   `/check-frameable?url=…` directly) against an allowlisted host and confirm you
   get a verdict (HTTP 200) rather than a 403 allowlist denial. This is the single
   most reliable confirmation that your configuration actually reached the
   backend — see the provisioning caveat in [§10](#10-known-operational-caveats).
6. **Review the tuning defaults** ([§4](#4-tuning)) and the monitoring setup
   ([§6](#6-metrics--suggested-alerts)) before putting the plugin in front of
   users.

---

## 2. Allowlist management

The allowlist is the plugin's primary SSRF / open-proxy containment. It is stored
in the plugin instance's **non-secret `jsonData`** under `allowedDomains`. These
are operational settings, not secrets, so they intentionally live in `jsonData`
(not `secureJsonData`).

### Where configuration lives

Settings are parsed **once**, when the plugin instance starts, from the plugin's
`jsonData` (`LoadSettings` in `pkg/plugin/config.go`, called from `NewApp`). There
are two ways to populate `jsonData`:

- **The plugin's configuration page** (Administration → Plugins → Web View →
  Configuration), which persists settings through Grafana's plugin-settings API
  (`POST /api/plugins/wilsonwaters-webview-app/settings`). Settings saved this way
  are read by the backend on the next instance start.
- **Provisioning** via a YAML file under `provisioning/plugins/` (see
  [§10](#10-known-operational-caveats) for an important caveat about provisioned
  `allowedDomains`).

> **Changing settings requires the plugin instance to reload.** The backend reads
> settings once at startup; after editing configuration (especially via
> provisioning) restart Grafana (or reload the plugin) so the new settings take
> effect, then re-verify with a Test URL.

### Allowlist shape

Each allowlist entry is a domain plus a per-domain `options` object:

```yaml
jsonData:
  allowedDomains:
    - domain: status.example.com        # bare hostname: no scheme, port, or path
      options:
        allowSubdomains: false          # default false (exact-match only)
        allowPrivateIP: false           # default false — see §3
        rateLimitPerMin: 0              # 0 = use the global per-domain default
        allowedPorts: []                # empty = standard 80/443 only
```

The equivalent JSON (`jsonData`) uses the same keys:
`allowedDomains[].domain`, and under `allowedDomains[].options`:
`allowSubdomains`, `allowPrivateIP`, `rateLimitPerMin`, `allowedPorts`.

> **Key casing matters.** The private-IP opt-in key is spelled **`allowPrivateIP`**
> (capital `IP`). The others are `allowSubdomains`, `rateLimitPerMin`,
> `allowedPorts`.

### Per-domain options

- **`domain`** — the bare hostname (e.g. `status.example.com`). No scheme, port,
  or path. An entry whose domain cannot be canonicalised is **skipped**, never
  matched loosely.
- **`allowSubdomains`** (default `false`) — when `false`, only the exact hostname
  matches. When `true`, subdomains also match, but only on a real label boundary
  (`api.example.com` matches `example.com`; the suffix trick
  `example.com.evil.com` does **not**). Prefer exact matches; broad apex +
  subdomain entries widen the SSRF surface.
- **`allowedPorts`** (default empty) — only ports **80 and 443** are permitted by
  default. List any additional TCP ports this domain may be reached on (e.g.
  `[8443]`). Keep this minimal.
- **`rateLimitPerMin`** (default `0`) — overrides the global per-domain rate limit
  for this domain only. `0` means "use the global default"
  (`rateLimitPerDomainPerMin`). See [§4](#4-tuning).
- **`allowPrivateIP`** (default `false`) — a deliberate, scoped SSRF opt-in.
  Covered in detail in [§3](#3-the-allowprivateip-opt-in).

---

## 3. The `allowPrivateIP` opt-in

`allowPrivateIP` is a **per-domain, admin opt-in** (default `false`) that
deliberately **relaxes the SSRF IP blocklist** so that domain may resolve to a
private address. Treat enabling it as consciously accepting a narrow SSRF surface
for that one domain.

**What it relaxes:** **RFC 1918 private IPv4 only** — `10.0.0.0/8`,
`172.16.0.0/12`, `192.168.0.0/16`. Nothing else.

**What it never relaxes (stays blocked even when opted in):**

- **Loopback** (`127.0.0.0/8`, `::1`)
- **Link-local**, **including the `169.254.169.254` cloud-metadata address**
  (`169.254.0.0/16`, `fe80::/10`)
- **CGNAT** (`100.64.0.0/10`), **IPv6 ULA** (`fc00::/7`), unspecified, multicast,
  reserved/documentation ranges, and the fail-closed unparseable sentinel.

Because loopback, link-local, and the metadata IP are never relaxed, this opt-in
cannot be used to reach cloud-metadata endpoints.

**Operational properties:**

- It is applied **narrowly** — only for the specific domain that matched, and
  **recomputed on every request and every redirect hop**.
- It is **audit-logged**: each admitted private IP produces a distinct `Warn`
  line, `proxy private-ip permitted by opt-in` (see [§5](#5-audit-logging)).
- It is **metered**: each admitted private IP increments
  `webview_proxy_private_ip_permitted_total{ipClass}` (see
  [§6](#6-metrics--suggested-alerts)).

**Guidance:** use it **sparingly**, only for trusted internal targets such as an
internal status board, and only after you have explicitly accepted the risk. Keep
the domain exact (avoid combining it with `allowSubdomains`). Monitor the permit
metric and the `Warn` audit line. For the full residual-risk analysis, see
[`docs/architecture.md`](./architecture.md#residual-risk-the-allowprivateip-opt-in-scoped-ssrf-surface).

---

## 4. Tuning

All of the following live in `jsonData`. Every security-relevant numeric field is
**fail-closed**: a missing, zero, or negative value is replaced with the safe
default below (`applyDefaults` in `pkg/plugin/config.go`), so you cannot
accidentally disable a limit by zeroing it — the default is reapplied.

| `jsonData` key | Default | What it controls |
| --- | --- | --- |
| `rateLimitPerInstancePerMin` | `60` | Max proxy requests per minute across the whole Grafana instance (token bucket). Over-limit → **429**. |
| `rateLimitPerDomainPerMin` | `30` | Max proxy requests per minute to any single target domain. Over-limit → **429**. Overridable per domain via `options.rateLimitPerMin`. |
| `maxConcurrentRequests` | `10` | Max in-flight proxy requests at once. Exceeded → **429**. |
| `maxResponseBytes` | `5242880` (5 MiB) | Max upstream response body size. Oversize → **413**. Also bounds the **gzip-decoded** size (decompression-bomb guard). |
| `requestTimeoutSec` | `10` | Per-request timeout in seconds covering the **whole** round-trip and body copy. Expiry → **504**. |
| `maxRedirects` | `3` | Max HTTP redirects followed for one request; each hop is re-validated against the allowlist and IP blocklist. **`0` disables redirect following** entirely. Exceeded → **502**. |

Notes:

- `maxRedirects` is special: an explicit `0` is honoured as "no redirects". Only an
  **absent** field falls back to the default of `3`; a negative value is clamped
  to `3`.
- For `maxResponseBytes`, `requestTimeoutSec`, the two rate limits, and
  `maxConcurrentRequests`, any value `<= 0` is treated as invalid and reset to the
  default — so the way to make a limit *looser* is to raise it, never to zero it.

**Tuning guidance:** start from the defaults. Raise `maxResponseBytes` only if you
proxy legitimately large pages; raise `requestTimeoutSec` for slow upstreams, but
keep it low enough that stalled fetches do not pile up against
`maxConcurrentRequests`. Raise the rate limits for high-traffic dashboards, and
prefer a per-domain `rateLimitPerMin` override to a global bump when only one busy
domain needs headroom.

---

## 5. Audit logging

The proxy emits **exactly one structured audit entry per request** (an `Info`
line, message `proxy request`) through the plugin backend's standard logger, with
these fields:

| Field | Meaning |
| --- | --- |
| `endpoint` | `proxy` or `proxy-resource` |
| `url` | The validated, normalised target (or a placeholder such as `<missing>` / `<invalid>` on an early denial) |
| `user` | The requesting Grafana login, or `anonymous` when no user is identifiable |
| `status` | Final HTTP status returned to the client |
| `bytes` | Response body bytes written |
| `duration` | Handler duration |
| `allowPrivateIP` | Whether the matched domain had the opt-in set for this request |

In addition, **each private IP admitted by the `allowPrivateIP` opt-in** produces a
distinct, high-signal `Warn` line — message `proxy private-ip permitted by opt-in`
— carrying `endpoint`, `url`, `user`, `permittedIP`, and `ipClass`. This line
fires only when an opted-in domain actually resolved to and reached a private
address, so it should be rare and worth investigating whenever it appears.

**Where to find it:** these entries go to the plugin backend's logs, which surface
in Grafana's server log output (and are captured by whatever log shipping your
deployment uses). Filter on the `proxy request` / `proxy private-ip permitted by
opt-in` messages, or on the `endpoint` / `status` / `user` fields.

---

## 6. Metrics & suggested alerts

The backend registers Prometheus collectors (`pkg/plugin/metrics.go`) on the
default registry; they are served on the plugin backend's standard metrics
endpoint via the Grafana plugin SDK's diagnostics adapter — scrape it the same way
as any plugin backend's metrics.

| Metric | Type | Labels | Meaning |
| --- | --- | --- | --- |
| `webview_proxy_requests_total` | counter | `status` | All proxy requests, by final HTTP status. |
| `webview_proxy_denials_total` | counter | `reason` | Denied requests, by denial reason (see below). |
| `webview_proxy_requests_in_flight` | gauge | — | Requests currently in flight. |
| `webview_proxy_request_duration_seconds` | histogram | — | Request handling duration. |
| `webview_proxy_private_ip_permitted_total` | counter | `ipClass` | Private IPs admitted by an `allowPrivateIP` opt-in, by relaxed IP class (today only `private`). |

**`denials_total{reason}` label values** (and the status each maps to):

| `reason` | Status | Cause |
| --- | --- | --- |
| `allowlist` | 403 | Host not allowlisted (incl. a redirect into a non-allowlisted host) |
| `ip-blocklist` | 403 | Resolved/connect IP is blocked |
| `metadata` | 403 | Cloud-metadata host |
| `rate-limit` | 429 | Per-instance or per-domain rate tier exceeded |
| `concurrency` | 429 | Concurrency cap exhausted |
| `size-limit` | 413 | Response exceeds `maxResponseBytes` |
| `scheme` | 400 | Bad scheme/port/userinfo/host |
| `bad-request` | 400 | Missing `url` / unbuildable target |
| `method` | 405 | Non-GET method |
| `timeout` | 504 | Per-request budget expired |
| `upstream` | 502 | Upstream resolve/transport/gateway failure |
| `redirect-loop` | 502 | Redirect depth cap exceeded |

### Suggested alerts (PromQL)

These are starting points — tune thresholds to your traffic.

- **SSRF / blocked-destination attempts spiking** (denials for internal targets):

  ```promql
  sum(rate(webview_proxy_denials_total{reason=~"ip-blocklist|metadata|allowlist"}[5m])) > 1
  ```

- **Any private-IP permit** (the opt-in actually reached a private address — expect
  this to be near-zero unless you intentionally run internal targets):

  ```promql
  sum(increase(webview_proxy_private_ip_permitted_total[15m])) > 0
  ```

- **Elevated upstream / gateway errors**:

  ```promql
  sum(rate(webview_proxy_requests_total{status=~"5.."}[5m]))
    / sum(rate(webview_proxy_requests_total[5m])) > 0.1
  ```

- **Sustained rate-limit / concurrency pressure** (may mean limits are too tight,
  or abuse):

  ```promql
  sum(rate(webview_proxy_denials_total{reason=~"rate-limit|concurrency"}[5m])) > 1
  ```

---

## 7. Incident response

If you suspect abuse or an SSRF attempt:

1. **Check the signals first.** Look at `webview_proxy_denials_total{reason="ip-blocklist"|"metadata"|"allowlist"}`
   and the `proxy private-ip permitted by opt-in` `Warn` lines / the
   `webview_proxy_private_ip_permitted_total` metric. The audit log's `user`,
   `url`, and `status` fields tell you who requested what and whether it was
   denied.
2. **Tighten the allowlist.** Remove any domain you did not intend to allow,
   remove `allowSubdomains` where exact matches suffice, and drop extra
   `allowedPorts`.
3. **Disable risky opt-ins.** Set `allowPrivateIP: false` on any domain that does
   not strictly require it; the private-IP surface closes immediately on the next
   instance reload.
4. **Throttle.** Lower `rateLimitPerInstancePerMin` / `rateLimitPerDomainPerMin`
   and `maxConcurrentRequests` to contain volume while you investigate.
5. **Contain hard if needed.** Disable the app (Administration → Plugins → Web
   View) to stop all proxying, or empty `allowedDomains` to fall back to deny-all.
6. **Apply and verify** — reload the plugin so new settings take effect, then
   re-verify with a Test URL.

Remember the hardcoded IP blocklist (loopback, link-local, the metadata IP, CGNAT,
ULA, reserved) **cannot be disabled or extended at runtime** and is not reachable
even when `allowPrivateIP` is set. If you believe you have found a bypass, follow
the reporting process in [`SECURITY.md`](../SECURITY.md).

---

## 8. IP blocklist (high-level)

The backend enforces a **hardcoded, non-configurable** IP blocklist on every
resolved address (`pkg/security/ipblocklist.go`). It cannot be disabled or extended
at runtime. It blocks:

- RFC 1918 private IPv4 (`10/8`, `172.16/12`, `192.168/16`)
- Loopback (`127/8`, `::1`)
- Link-local, including the `169.254.169.254` cloud-metadata address
  (`169.254/16`, `fe80::/10`)
- CGNAT (`100.64/10`), IPv6 ULA (`fc00::/7`), unspecified, multicast, and
  reserved/documentation ranges

Unparseable or unclassifiable addresses **fail closed** (treated as blocked). The
blocklist runs as part of a resolve-then-dial flow that also re-validates the
concrete connect-time IP, defeating DNS rebinding, and blocks known cloud-metadata
hostnames by name before resolution. The only authorised relaxation is the narrow,
per-domain `allowPrivateIP` opt-in ([§3](#3-the-allowprivateip-opt-in)), which
covers RFC 1918 IPv4 only. For the full mechanism (SF1 and SF4), see
[`docs/architecture.md`](./architecture.md#4-layered-security-controls-sf1sf5).

---

## 9. Interaction with Grafana's `[security.egress]` controls

The plugin's allowlist and IP blocklist are **defense-in-depth alongside**, **not a
replacement for**, Grafana's own network-level egress controls (`[security.egress]`
in Grafana's configuration). Both layers apply to the requests the backend makes:

- Grafana's `[security.egress]` allowlist (where configured) restricts which hosts
  the Grafana process and its plugins may reach at the network layer.
- The plugin's `allowedDomains` is a second, plugin-level allowlist on top of that.

A target must pass **both** to be fetched. The plugin does not bypass, loosen, or
substitute for `[security.egress]`; if you operate an egress allowlist, make sure
the domains you allowlist in this plugin are also permitted by your
`[security.egress]` policy, otherwise the fetch fails at the network layer even
though the plugin would permit it. See the non-goals in
[`SECURITY.md`](../SECURITY.md) for the project's stated posture.

---

## 10. Upgrade & rollback

- **Plugin upgrades / downgrades:** install the target version and restart
  Grafana. Settings in `jsonData` carry across versions (same keys).
- **`plugin.json` changes require a Grafana server restart.** Any change to the
  plugin manifest only takes effect after restarting Grafana.
- **Signed builds:** installs must satisfy Grafana's plugin-signing requirements.
  Private-signed builds are valid only for the Grafana root URLs they were signed
  for, so a rollback to a build signed for different root URLs may fail to load.
  See [`docs/signing.md`](./signing.md) for signing, root-URL scoping, and secret
  handling.
- **After any upgrade/rollback,** re-verify the allowlist took effect with a Test
  URL, and confirm metrics are still being scraped.

---

## 11. Known operational caveats

- **Provisioned `allowedDomains` may not reach the backend in all setups (#113).**
  Provisioning the allowlist through `jsonData.allowedDomains` in a
  `provisioning/plugins/*.yaml` file does **not** reliably deliver the allowlist to
  the backend in every Grafana setup. **Prefer configuring the allowlist through
  the running plugin** (the app configuration page / the plugin settings API),
  which the backend reads at instance start, and **always verify** afterwards: run
  a **Test URL** against an allowlisted host, or call `/check-frameable?url=…`, and
  confirm you get a verdict (HTTP 200) rather than a `403` allowlist denial. A
  403 on a host you believe you allowlisted is the signature of this caveat — the
  configuration did not reach the backend.

- **In-panel proxy rendering is deferred for v1 (FR5 / #102).** The backend proxy
  pipeline (`/proxy`, `/proxy-resource`, `/check-frameable`) is complete and
  security-tested, but **in-panel rendering of proxy-mode content** is deferred:
  Grafana serves plugin resource routes with `X-Frame-Options: deny` and a
  sandboxing CSP, which prevents the panel iframe from framing the `/proxy` URL
  directly. **Direct mode is unaffected.** Do not rely on in-panel proxy-mode
  rendering in v1. See
  [`docs/architecture.md`](./architecture.md#7-known-limitation-in-panel-proxy-rendering-fr5--102).
