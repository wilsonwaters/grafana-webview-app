# Security Policy

This plugin ships a Go backend proxy that can fetch external URLs on behalf of a
dashboard author, so security is a first-class concern. This document is the
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

The plugin is in active development and has not yet had a stable release. Until
`1.0.0` is published, security fixes are applied to the default development
branch only.

| Version            | Supported             |
| ------------------ | --------------------- |
| `main` (`develop`) | :white_check_mark:    |
| Pre-release builds | :warning: best effort |

This table will be updated once tagged releases exist.

## Scope

**In scope:** the plugin's frontend code, the Go backend, and the backend's
request-handling endpoints (`/proxy`, `/proxy-resource`, `/check-frameable`,
`/health`). Because the backend can fetch external URLs on behalf of a dashboard
author, its request-validation and access-control behaviour — SSRF containment,
DNS-rebinding defense, header handling, and resource limits — is of particular
interest.

**Out of scope:** vulnerabilities in Grafana itself, in third-party sites the
plugin is configured to display, or in the deployment environment. Report those
to the respective projects or vendors.

## Security posture summary

The backend proxy is designed to survive a Grafana Labs catalog security review.
It is **stateless and unauthenticated** and every control is **on by default and
fails closed**. The layered controls (detailed in
[`docs/architecture.md`](./docs/architecture.md)) are:

- **Deny-all allowlist by default** — a fresh install proxies nothing; an admin
  must explicitly allowlist domains. An empty allowlist denies every request.
- **Hardcoded, non-configurable IP blocklist** — RFC 1918 private, loopback,
  link-local (including the `169.254.169.254` cloud-metadata address), CGNAT,
  IPv6 ULA, unspecified, multicast, and reserved ranges. Cannot be disabled or
  extended at runtime. Unparseable addresses fail closed.
- **DNS-rebinding-protected dialler** — resolve all records, validate every one
  against the IP blocklist (reject the whole request if any is blocked), dial the
  validated IP literal directly, and re-validate the concrete connect IP at the
  socket layer. Cloud-metadata hostnames are blocked by name before resolution.
- **Scheme/port restrictions** — only `http`/`https`, only ports 80/443 (plus any
  per-domain extra ports an admin configures); embedded credentials rejected.
- **Header stripping, both directions** — outgoing requests are stripped of
  cookies, `Authorization`, forwarding/client-IP headers, and all `X-Grafana-*`
  identity; incoming responses are stripped of `Set-Cookie`,
  `Strict-Transport-Security`, `Public-Key-Pins`, `Clear-Site-Data`, and framing
  headers (`X-Frame-Options`, CSP `frame-ancestors`).
- **Resource limits** — per-instance and per-domain rate limits, an in-flight
  concurrency cap, a response-size limit (bounding both wire and gzip-decoded
  size), and a per-request timeout covering the full round-trip.
- **Content safety** — HTML is parsed (never regex) and rewritten so subresources
  re-enter the pipeline; frame-buster scripts and CSP/refresh metas are
  neutralised; the view iframe always uses `sandbox="allow-scripts
  allow-same-origin"` and is never broadened; author hide-selectors are validated
  and cannot inject markup.
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
respectively. The full threat table (assets, actors, threat → mitigation →
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

The plugin fails closed, but two decisions are the administrator's to make and
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

## Known limitation

In-panel rendering of **proxy-mode** content is deferred for v1 (tracked as #102):
Grafana's resource-route response headers (`X-Frame-Options: deny` + `CSP:
sandbox`) block the panel from framing the proxy URL directly. This is a
front-end rendering limitation only — the backend proxy pipeline is complete and
security-tested. Direct mode is unaffected. See
[`docs/architecture.md`](./docs/architecture.md#7-known-limitation-in-panel-proxy-rendering-fr5--102).
