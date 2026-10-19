# Security Policy

This plugin ships a Go backend proxy that can fetch external URLs on behalf of
dashboard users, so security is a first-class concern. This document is the
public-facing policy: how to report a vulnerability, what is in scope, the
security posture summary, and admin responsibilities.

For the full, implementation-grounded **threat model, layered controls (SF1–SF5),
and backend handler reference**, see [`docs/architecture.md`](./docs/architecture.md).
For operational configuration and tuning, see
[`docs/administration.md`](./docs/administration.md).

## Reporting a vulnerability

**Please do not report security vulnerabilities through public GitHub issues,
discussions, or pull requests.**

Instead, report them privately through GitHub Security Advisories:

- https://github.com/wilsonwaters/grafana-webview-app/security/advisories/new

Use the "Report a vulnerability" flow. This creates a private channel visible
only to the reporter and the maintainers.

When reporting, please include as much of the following as you can:

- A description of the issue and its potential impact
- The plugin version and Grafana version affected
- Steps to reproduce, or a proof of concept
- Any relevant configuration (e.g. deployment method, allowlist entries)
- Suggested remediation, if you have one

Please give us a reasonable opportunity to investigate and remediate before any
public disclosure.

## Response expectations

As a community project we aim to:

- Acknowledge a report within **5 business days**
- Provide an initial assessment within **10 business days**
- Keep the reporter informed of remediation progress

These are targets, not guarantees, and may vary with maintainer availability.

## Supported versions

Security fixes are released for the latest `1.x` version. The `0.0.1` alpha was
never published (its release run failed) and is not supported.

| Version            | Supported             |
| ------------------ | --------------------- |
| `1.0.x`            | :white_check_mark:    |
| `main`             | :white_check_mark:    |
| `0.0.1` and other pre-release builds | :x:  |

## Scope

**In scope:** the plugin's frontend code, the Go backend, and the backend's
request-handling endpoints (`/proxy`, `/check-frameable`, `/health`). Because the
backend can fetch external URLs on behalf of dashboard users, its
request-validation and access-control behaviour — SSRF containment,
DNS-rebinding defense, header handling, resource limits, and the isolation of
proxied pages from Grafana — is of particular interest.

**Out of scope:** vulnerabilities in Grafana itself, in third-party sites the
plugin is configured to display, or in the deployment environment. Report those
to the respective projects or vendors.

## Security posture summary

The backend proxy is designed to survive a Grafana Labs catalog security review.
It is **stateless** (it sends no credentials or sessions upstream) and every
control is **on by default and fails closed**. The layered controls (detailed in
[`docs/architecture.md`](./docs/architecture.md)) are:

- **Deny-all allowlist by default** — a fresh install proxies nothing; an admin
  must explicitly allowlist domains on the Configuration page. An empty
  allowlist denies every request.
- **Access control** — Grafana requires a signed-in user for every backend call.
  `/proxy` is open to every user of the organisation (Viewers included, and
  anonymous viewers if anonymous access is on), so the allowlist and the rate
  limits are its access control. `/check-frameable` (**Test URL**) requires the
  Editor or Admin org role. The Configuration page is for org Admins.
- **Hardcoded, non-configurable IP blocklist** — RFC 1918 private, loopback,
  link-local (including the `169.254.169.254` cloud-metadata address), CGNAT,
  IPv6 ULA, unspecified, multicast, and reserved ranges. 6to4 and NAT64
  well-known-prefix addresses are classified by the IPv4 address they carry.
  Cannot be disabled or extended at runtime. Unparseable addresses fail closed.
- **DNS-rebinding-protected dialler** — resolve all records, validate every one
  against the IP blocklist (reject the whole request if any is blocked), dial the
  validated IP literal directly, and re-validate the concrete connect IP at the
  socket layer. Cloud-metadata hostnames are blocked by name before resolution.
- **Scheme/port restrictions** — only `http`/`https`, only ports 80/443 (plus any
  per-domain extra ports an admin configures); embedded credentials rejected.
- **Header stripping, both directions** — outgoing requests are stripped of
  cookies, `Authorization`, forwarding/client-IP headers, and all `X-Grafana-*`
  identity; incoming responses are stripped of `Set-Cookie`,
  `Strict-Transport-Security`, `Public-Key-Pins`, `Clear-Site-Data`, `Refresh`,
  every `Access-Control-*` header, and framing headers (`X-Frame-Options`, CSP
  `frame-ancestors`). The proxy sends no CORS headers.
- **Resource limits** — per-organisation and per-domain rate limits, an
  in-flight concurrency cap, a response-size limit (bounding the wire,
  gzip-decoded and rewritten size; an oversize response is always a 413, never
  a truncated page), and a per-request timeout covering the full round-trip.
  Compressed responses in an encoding other than gzip are refused.
- **Panel URL validation** — the panel loads only absolute `http://` and
  `https://` URLs. Other schemes (`javascript:`, `data:`, …), relative URLs and
  URLs with credentials or control characters show an error instead of a frame.
- **Isolation of proxied pages** — the panel renders proxied HTML in a `srcdoc`
  iframe with `sandbox="allow-scripts"` only. The page runs in an opaque origin
  and cannot read the Grafana session or call the Grafana API. Direct-mode
  frames get `allow-same-origin` only for a cross-origin URL. No frame gets
  `allow-popups`, `allow-forms`, `allow-modals` or `allow-top-navigation`.
- **Content safety** — HTML is parsed (never regex) and rewritten with absolute
  upstream URLs and a `no-referrer` policy; frame-buster scripts and CSP/refresh
  metas are neutralised; author hide-selectors are validated and cannot inject
  markup.
- **Auditability** — one structured audit log entry per request and Prometheus
  metrics for requests, denials by reason, in-flight count, and duration.
- **Non-skippable security test suite** — the security behaviours are covered by
  tests that run on every PR in CI and cannot be skipped.

### Threat model summary

The proxy could, if unmitigated, be abused for **SSRF** (reaching internal/cloud-
metadata services), **open-proxy / egress abuse**, **clickjacking**, **DNS
rebinding**, and **resource exhaustion**. The deny-all allowlist, hardcoded IP
blocklist, resolve-then-dial with a connect-time rebind guard, scheme/port and
size/rate/concurrency limits, and framing-header stripping mitigate each of these
respectively. On the frontend, embedded pages could try to **run script in the
Grafana origin**; URL scheme validation and the per-mode iframe sandbox prevent
this. The full threat table (assets, actors, threat → mitigation →
residual risk) is in [`docs/architecture.md`](./docs/architecture.md#6-threat-model).

### Explicit non-goals

- The proxy **does not forward credentials** — no cookies, no `Authorization`, no
  `X-Grafana-*` identity reaches an upstream.
- The proxy **does not maintain sessions**, proxy WebSockets/SSE, or execute
  proxied JavaScript server-side.
- The proxy **does not replace Grafana's own `[security.egress]` controls** — it
  is defense-in-depth alongside, not a substitute for, network-level egress
  policy.

## Admin responsibilities

The plugin fails closed, but these decisions are the administrator's to make and
own:

1. **The allowlist is deny-all by default.** Nothing is proxied until you add
   domains. Add only domains you trust; prefer exact matches over
   `allowSubdomains`, and avoid broad apex+subdomain entries that widen the SSRF
   surface. See [`docs/administration.md`](./docs/administration.md).

2. **`AllowPrivateIP` is a deliberate, scoped SSRF risk.** This per-domain opt-in
   (default **off**) relaxes the IP blocklist for **RFC 1918 IPv4 only** — it does
   **not** relax loopback, link-local, the `169.254.169.254` metadata address,
   CGNAT, ULA, or reserved ranges, which stay blocked. Enabling it on a domain
   means that domain can be pointed at an internal private-network service it
   resolves to. Enable it only for internal-status-board use cases where you have
   explicitly accepted that risk. Each admitted private IP is logged (a distinct
   `Warn` audit line) and counted in the
   `webview_proxy_private_ip_permitted_total` metric — monitor both.

3. **Every signed-in user can use the proxy for allowlisted sites.** Allowlist
   only sites that every user of the organisation may see. The allowlist is not
   confidential: any signed-in user can find out whether a host is on it. If you
   turn on Grafana's anonymous access, anonymous viewers can use the proxy too,
   within the rate limits.

4. **The bundled Docker Compose setup is for local development only.** It signs
   every visitor in as an anonymous Admin, runs Grafana in development mode and
   loads the unsigned plugin. Never expose it to an untrusted network or reuse
   its settings in production. Production installs use the signed release
   archive and start with an empty allowlist. See
   [`docs/installation.md`](./docs/installation.md#8-do-not-use-the-development-setup-in-production).

## Known limitations

- **Test URL uses the basic org role.** A user who can edit dashboards only
  through a custom RBAC role cannot run the frameability check.
- **Operator-specific NAT64 prefixes** (RFC 6052 network-specific prefixes)
  cannot be detected. Block them with network egress policy if your network
  uses one.
- **Proxied subresources load in the viewer's browser.** The upstream site sees
  the viewer's IP address (but no `Referer`) for images, scripts and
  stylesheets. See
  [`docs/architecture.md`](./docs/architecture.md#7-proxy-mode-rendering-fr5)
  for the other functional limits of proxy mode.
