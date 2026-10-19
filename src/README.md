# Webview

Embed external web pages in Grafana dashboard panels, and choose exactly which
part of the page viewers see.

[![License](https://img.shields.io/github/license/wilsonwaters/grafana-webview-app)](https://github.com/wilsonwaters/grafana-webview-app/blob/main/LICENSE)
[![Grafana](https://img.shields.io/badge/Grafana-%3E%3D12.4.0-orange)](https://grafana.com)

## Overview

Webview is an app plugin with a **Web View** panel. The panel shows a web page
inside a dashboard, for example a weather radar, a status page or an internal
tool. In the panel editor you drag and zoom a live preview to frame the region
you want. Viewers then see that region, without scrolling or clicking.

The panel loads a page in one of two ways:

- **Direct mode** — a sandboxed iframe that points at the page. For sites that
  allow framing. No backend is involved.
- **Proxy mode** — for sites that block framing with `X-Frame-Options` or a
  `frame-ancestors` policy. The plugin's backend fetches the page, removes the
  framing headers, and the panel shows it in an isolated sandboxed frame. It
  works on a standard Grafana install, with no change to `allow_embedding`.

A **Test URL** button checks whether a page can be framed directly and picks
the mode for you.

![Proxy-mode, direct-mode and error panels on one dashboard](https://raw.githubusercontent.com/wilsonwaters/grafana-webview-app/main/docs/screenshots/issue-102/grafana-13.2.4-chromium-dashboard.png)

## Requirements

- Grafana **12.4.0 or later** (tested on 12.4, 13.0, 13.1 and 13.2).
- For proxy mode, a Grafana installation that can run backend plugins. Direct
  mode works without the backend.
- For proxy mode, viewers' browsers must be able to reach the proxied site:
  the page's images, scripts and stylesheets load from that site directly.

## Getting started

1. Install the plugin and restart Grafana. See the
   [Installation guide](https://github.com/wilsonwaters/grafana-webview-app/blob/main/docs/installation.md).
2. Enable the app in **Administration → Plugins and data → Plugins → Web View**.
3. **Add the domains you want to proxy.** Open the app's **Configuration** tab,
   click **Add domain**, enter the host name (for example `status.example.com`)
   and click **Save settings**. The change applies to the next request; no
   restart is needed.
4. Edit a dashboard, add a visualization and choose **Web View**.
5. Enter the page URL, click **Test URL**, and drag and scroll the preview to
   frame the region you want. Save the dashboard.

![The Configuration page with one allowlisted domain and the global limits](https://raw.githubusercontent.com/wilsonwaters/grafana-webview-app/main/docs/screenshots/issue-129/config-13.2.4-light.png)

## The allowlist

The proxy is **deny-all by default**: a new installation proxies nothing until
an administrator adds domains. The **Test URL** check uses the same list.

Every signed-in user of the organisation, including Viewers, can load
proxy-mode panels, so the allowlist and the rate limits are the access control
for the proxy. Allowlist only sites that every user may see. The backend also
blocks private, loopback, link-local and cloud-metadata addresses, and limits
request rate, concurrency, response size and time. See the
[Administration guide](https://github.com/wilsonwaters/grafana-webview-app/blob/main/docs/administration.md).

## Known limitations

Proxy mode sends only the HTML page through Grafana. Because of this:

- If Grafana runs with `[security] content_security_policy = true`, proxied
  pages show markup and images only; their scripts do not run.
- The page runs in an isolated origin. Its cookies and storage last only until
  the next reload, and single-page apps that call their own APIs do not work.
- Sites with bot protection (for example Akamai or reCAPTCHA) can block the
  page.
- Pages that need a login do not work; the proxy sends no credentials.
- The viewer's browser loads the page's images, scripts and stylesheets directly
  from the site, so it must be able to reach the site. An internal site that only
  the Grafana server can reach shows without styles or images.
- Live updates over WebSockets or Server-Sent Events often fail; use the panel's
  auto-refresh instead.

Direct mode has none of these limits, but works only for sites that allow
framing. See
[Which sites work](https://github.com/wilsonwaters/grafana-webview-app#which-sites-work)
for a table of site types and modes, and the
[Troubleshooting guide](https://github.com/wilsonwaters/grafana-webview-app/blob/main/docs/troubleshooting.md)
to recognise each case.

## Documentation

- [Configuration guide (panel options)](https://github.com/wilsonwaters/grafana-webview-app/blob/main/docs/configuration.md)
- [Administration guide](https://github.com/wilsonwaters/grafana-webview-app/blob/main/docs/administration.md)
- [Installation guide](https://github.com/wilsonwaters/grafana-webview-app/blob/main/docs/installation.md)
- [Troubleshooting](https://github.com/wilsonwaters/grafana-webview-app/blob/main/docs/troubleshooting.md)
- [Security policy](https://github.com/wilsonwaters/grafana-webview-app/blob/main/SECURITY.md)
- [Changelog](https://github.com/wilsonwaters/grafana-webview-app/blob/main/CHANGELOG.md)

## Feedback and contributing

Report bugs and request features in
[GitHub issues](https://github.com/wilsonwaters/grafana-webview-app/issues).
Report security problems privately through
[GitHub Security Advisories](https://github.com/wilsonwaters/grafana-webview-app/security/advisories/new).
See the [contributing guide](https://github.com/wilsonwaters/grafana-webview-app/blob/main/CONTRIBUTING.md).
