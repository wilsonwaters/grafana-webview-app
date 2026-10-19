# Example dashboard

This folder contains an importable Grafana dashboard that demonstrates the
**Web View** panel (`wilsonwaters-webview-panel`) in both load modes.

- **File:** [`webview-example-dashboard.json`](webview-example-dashboard.json)

## What it demonstrates

| Panel | Load mode | What you should see |
|-------|-----------|---------------------|
| **Direct mode — renders in-panel** | `direct` (`url: https://example.com`) | The external page rendered directly inside the panel. Needs no backend and no allowlist. |
| **Proxy mode — requires allowlisting** | `proxy` (`url: https://example.org`) | The page fetched through the backend proxy and rendered inside the panel in an isolated frame, once `example.org` is allowlisted. Until then the panel shows *"The page could not be loaded through the backend proxy (HTTP 403)"* with the reason *"target host is not allowlisted"*. The debug overlay shows the resolved mode. |

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
adds its target domain (`example.org`) on the app's **Configuration** page
(**Administration → Plugins and data → Plugins → Web View → Configuration**). The
change applies to the next request; reload the dashboard to see the page. See
[Administration → Allowlist management](../administration.md#2-allowlist-management).

> **Note:** this example is intentionally **not** auto-provisioned with a
> pre-allowlisted domain. Production installs start with an empty allowlist, so
> import the dashboard here and allowlist the domain yourself.
