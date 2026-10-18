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
  so a Viewer cannot use it to learn which hosts are allowlisted.
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

## Doc notes for DOC1

- README line 22 promises "Grafana RBAC checks" on the proxy. Replace it with the model above.
- `docs/administration.md` audit table: `endpoint` is `proxy` or `check-frameable` (`proxy-resource` no
  longer exists). Add the `role` denial reason (403).
- `docs/architecture.md`: remove `/proxy-resource`, `/ping` and `/echo`.
