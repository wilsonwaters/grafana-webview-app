# Authorization model for the backend resource routes (VR-3, S2 / #142)

**Decided:** 2026-10-19, S2. **Input for:** DOC1 (`SECURITY.md`, administration and architecture docs).

## What Grafana does

Grafana authenticates every `/api/plugins/wilsonwaters-webview-app/resources/*` call and checks basic
plugin access. It does **not** apply the `role` of an `includes[]` entry in `plugin.json` to resource
calls; that role only hides the configuration page in the navigation. Any authorization beyond
"signed in to the org" is the plugin's job. The plugin reads the caller from
`backend.PluginConfigFromContext(ctx).User` (`Login`, `Role` = the basic org role).

## Decision

| Route | Who can call it | Why |
|-------|-----------------|-----|
| `GET /proxy` | Any signed-in org user (Viewer and up) | Dashboard viewers must be able to render proxy-mode panels. |
| `GET /check-frameable` | Org role **Editor** or **Admin** | Only the panel editor's "Test URL" button uses it. |
| `GET /health` | Any signed-in org user | Liveness only; no pipeline, no settings, no upstream. |
| `/proxy-resource`, `/echo`, `/ping` | Removed (404) | No caller after FR5; scaffold leftovers (VR-8). |

- `/check-frameable` checks the role **first**, before the method, URL, allowlist and rate-limit gates,
  so a Viewer gets the role denial and nothing else from it. This does not keep the allowlist secret:
  `/proxy` is open to every signed-in user and answers a host that is not allowlisted with its own
  403 (`target host is not allowlisted`), so any signed-in user can find out whether a given host is
  allowlisted. **The allowlist is not confidential**; do not put anything in it (host names of
  internal systems, for example) that org users must not learn.
- A missing user, an empty role, `Viewer`, `None` or any unknown role gets **403** with a JSON body
  `{"error":"Forbidden","reason":"role","message":"..."}`. The denial is audited (one `proxy request` log
  line, `endpoint=check-frameable`) and counted (`webview_proxy_denials_total{reason="role"}`).
- `/check-frameable` is now audited and metered for every request, the same way as `/proxy`.
- The check uses the **basic** org role. A user whose basic role is Viewer but who can edit
  dashboards through a custom RBAC role cannot use "Test URL". Accepted for v1.0.0.

## What protects `/proxy`

`/proxy` has no role gate. Its controls are: the admin-managed domain allowlist (empty by default, so a
fresh install proxies nothing), the hardcoded IP blocklist with resolve-then-dial, the scheme and port
checks, the per-instance and per-domain rate limits, the concurrency cap, the response size limit, and
the audit log (the `user` field records the Grafana login). Any signed-in user can fetch any allowlisted
URL through it. Admins must allowlist only hosts that every user of the org may read.

### IPv6 addresses that carry an IPv4 address (NAT64, 6to4)

- An address in the NAT64 well-known prefix `64:ff9b::/96` (RFC 6052) or in 6to4 `2002::/16` is
  classified by the IPv4 address it embeds. So `64:ff9b::` + a public IPv4 address is **allowed**
  (this is what DNS64 returns for every IPv4-only site on an IPv6-only host), and `64:ff9b::` + a
  blocked IPv4 address (for example `64:ff9b::a9fe:a9fe`, the metadata address 169.254.169.254) is
  blocked. `allowPrivateIP` never relaxes an RFC 1918 address wrapped in either prefix.
- The NAT64 local-use prefix `64:ff9b:1::/48` (RFC 8215) is always blocked.
- **Limitation:** a network-specific NAT64 prefix that an operator chose (any other /32 to /96
  prefix, RFC 6052 section 2.2) cannot be recognised. An address in it that the operator's
  translator maps to an internal IPv4 address looks like an ordinary public IPv6 address and is
  allowed. On a host behind such a NAT64, the IP blocklist does not protect IPv4 targets reached
  through the translator; only the allowlist does.

## Doc notes for DOC1

- README line 22 promises "Grafana RBAC checks" on the proxy. Replace it with the model above.
- `docs/administration.md` audit table: `endpoint` is `proxy` or `check-frameable` (`proxy-resource` no
  longer exists). Add the `role` denial reason (403).
- `docs/architecture.md`: remove `/proxy-resource`, `/ping` and `/echo`.
- Other S2/FR5 backend changes to document: `/proxy` and `/check-frameable` send no CORS headers and
  strip upstream `Access-Control-*` headers; `/proxy` reads the whole body before replying, so any
  response (including a gzip body after decoding, and the rewritten HTML) over `maxResponseBytes` is a
  413, never a truncated 200; a `Content-Encoding` other than gzip is a 502; the `Refresh` header is
  stripped; 6to4 and NAT64 addresses are classified by their embedded IPv4 address (see the NAT64
  section above, including the operator-prefix limitation).
- RV2-be changes to document: the allowlist is not confidential (see above); a followed redirect's
  `Location` is now relative (`proxy?url=...&_wvredir=N`, plus the request's `hide` parameters) so
  it works under a Grafana sub-path; HTML that cannot be parsed (for example nested deeper than 512
  elements) is a 502, never the un-rewritten page; an empty or bodiless response with
  `Content-Encoding: gzip` passes through; `/check-frameable` and `/proxy` return only a reason
  (never the dial error with resolved IPs) for an unreachable upstream; the audit `url` field has
  its query values redacted; the parse limits and memory sizing are in `memory-sizing.md`.
