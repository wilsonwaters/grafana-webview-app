# Example dashboard

This folder contains an importable Grafana dashboard that demonstrates the
**Web View** panel (`wilsonwaters-webview-panel`) in both load modes.

- **File:** [`webview-example-dashboard.json`](webview-example-dashboard.json)

## What it demonstrates

| Panel | Load mode | What you should see |
|-------|-----------|---------------------|
| **Direct mode — renders in-panel** | `direct` (`url: https://example.com`) | The external page rendered directly inside the panel. Needs no backend and no allowlist. |
| **Proxy mode — requires allowlisting** | `proxy` (`url: https://example.org`) | The documented proxy-mode fallback. In-panel proxy rendering is deferred for v1 ([#102](https://github.com/wilsonwaters/grafana-webview-app/issues/102)), and the backend proxies nothing until the domain is allowlisted (see the prerequisite below). |

A text panel at the top of the dashboard repeats these notes in-product.

## How to import

1. In Grafana, go to **Dashboards → New → Import**.
2. Upload `webview-example-dashboard.json` (or paste its contents), then click
   **Load** and **Import**.
3. Requires the **Web View** plugin (`wilsonwaters-webview-app`) to be installed
   and enabled. See the [Installation guide](../installation.md).

The dashboard uses a fixed `uid` of `webview-example`; change it on import if that
uid is already taken in your instance.

## Prerequisite for the proxy panel

Proxy mode is **fail-closed**. The proxy domain allowlist is **empty by default
(deny-all)**, so the proxy panel will not fetch anything until an administrator
adds its target domain (`example.org`) to the allowlist. See
[Administration → Allowlist management](../administration.md#2-allowlist-management).

Even once the domain is allowlisted, in-panel rendering of proxied content is
deferred for v1 ([#102](https://github.com/wilsonwaters/grafana-webview-app/issues/102)):
the backend proxy pipeline runs and is verified, but the panel shows the
documented fallback rather than the proxied page. The direct-mode panel is the one
that visibly renders in this release.

> **Note:** this example is intentionally **not** auto-provisioned with a
> pre-allowlisted domain. Shipping a demo domain in product configuration via
> `provisioning/` is unreliable for backend delivery
> ([#113](https://github.com/wilsonwaters/grafana-webview-app/issues/113)) and we
> do not bake demo domains into shipped config. Import the dashboard here and
> allowlist the domain yourself.
