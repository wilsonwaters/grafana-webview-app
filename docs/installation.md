# Installation Guide

How to install the **Web View** app plugin (`wilsonwaters-webview-app`) into a
self-hosted Grafana deployment.

This plugin is distributed as a **private, self-hosted plugin (Path 1)**. It is
**not** in the Grafana plugin catalog, so you do **not** install it with
`grafana-cli plugins install` or from the in-app plugin catalog. You install it
manually from a released, privately-signed archive (or build it from source),
place it in Grafana's plugins directory, and restart Grafana.

> **Catalog install (Path 2) is out of scope for v1.** Submission to the Grafana
> plugin catalog for Community signing requires Grafana Labs review and has not
> started (see [`docs/signing.md`](./signing.md#community-signing--catalog-submission-path-2)).
> Until then, a public/community signature is rejected (HTTP 409) and only the
> self-hosted install methods below apply.

For what to do **after** install, and for operating the plugin safely, see the
[Administration guide](./administration.md). For signing mechanics and secrets,
see [`docs/signing.md`](./signing.md). For the architecture and threat model, see
[`docs/architecture.md`](./architecture.md). For a feature overview, see the
[README](../README.md).

---

## Contents

- [1. Prerequisites](#1-prerequisites)
- [2. Signed vs unsigned, and the `rootUrls` requirement](#2-signed-vs-unsigned-and-the-rooturls-requirement)
- [3. Install methods](#3-install-methods)
  - [3.1 Bare-metal / VM (download the signed release)](#31-bare-metal--vm-download-the-signed-release)
  - [3.2 Docker](#32-docker)
  - [3.3 Helm / Kubernetes](#33-helm--kubernetes)
  - [3.4 Build from source](#34-build-from-source)
- [4. Required first-time step: allowlist at least one domain](#4-required-first-time-step-allowlist-at-least-one-domain)
- [5. Verify the install](#5-verify-the-install)
- [6. Restart requirements](#6-restart-requirements)
- [7. Path 2 (Grafana Cloud catalog) — not yet available](#7-path-2-grafana-cloud-catalog--not-yet-available)
- [8. Do not use the development setup in production](#8-do-not-use-the-development-setup-in-production)

---

## 1. Prerequisites

| Requirement | Detail |
| --- | --- |
| **Grafana** | **>= 12.4.0** (`grafanaDependency` in `src/plugin.json`). Tested on 12.4, 13.0, 13.1 and 13.2. Proxy mode needs no change to Grafana's `allow_embedding` or cookie settings. |
| **Browsers** | Tested with Playwright Chromium, Firefox and WebKit builds. For proxy mode, viewers' browsers must be able to reach the proxied sites, because images, scripts and stylesheets load from the site directly. |
| **Backend-plugin support** | This is an **app plugin with a Go backend** (`"backend": true`, executable `gpx_webview`). The Grafana host must be able to **run the plugin binary** as a subprocess. Hosted/managed Grafana that does not permit backend plugins can only use **Direct mode** (see below). |
| **Platform binary** | The released archive bundles the backend executable. Install on a host whose OS/architecture matches the packaged binary, or build from source for your platform (see [§3.4](#34-build-from-source)). |
| **Install permission** | Filesystem access to Grafana's plugins directory and the ability to restart Grafana (or your orchestration equivalent). |
| **Memory** | The plugin's backend idles at a few MB; see the [Administration guide](administration.md) for its worst case. Grafana 12.4+ itself needs much more memory than older versions (see the small-host note below). |

> **Small hosts (for example a 1 GB Raspberry Pi).** Grafana 13 on its own can
> grow to about 300 MB, roughly three times Grafana 9, and starts one process per
> bundled data-source plugin. On a host that also runs databases and has no swap,
> that can trigger the out-of-memory killer in a loop. The plugin's backend is not
> the cause. On a 1 GB Pi, two Grafana settings cut Grafana's start-up memory
> from about 184 MB to 120 MB and its plugin processes from 15 to 2:
>
> - Disable the bundled data-source plugins you do not use, for example
>   `GF_PLUGINS_DISABLE_PLUGINS=elasticsearch,loki,tempo,mssql,…` (or
>   `[plugins] disable_plugins` in `grafana.ini`). Keep any data source a
>   dashboard uses.
> - Set `GOMEMLIMIT` for the Grafana server process, for example
>   `Environment=GOMEMLIMIT=220MiB` in a systemd drop-in for `grafana-server`.

**Proxy mode needs the backend; Direct mode does not.** The plugin degrades
gracefully: if no backend is available (for example a Grafana deployment without
backend-plugin support), the panel still works in **direct-iframe-only** mode and
the proxy option and "Test URL" are cleanly disabled in the editor. Backend
availability is detected at runtime via the `/health` resource. If you need
proxy mode, you must install on a backend-capable host.

> **Note the second plugin ID.** The app (`wilsonwaters-webview-app`) bundles a
> **nested Web View panel** that registers under its *own* plugin id,
> `wilsonwaters-webview-panel`. When you allow unsigned loading (see §2), you must
> list **both** ids, or the panel will not load even though the app does.

---

## 2. Signed vs unsigned, and the `rootUrls` requirement

Grafana enforces plugin signatures. There are two supported ways to satisfy it
for a self-hosted install:

### Privately-signed (recommended)

The release build is **privately signed** for specific Grafana instance URLs
(`rootUrls`). The signature is produced by the release workflow
(`.github/workflows/release.yml`), which runs the `sign` script in
`package.json`. That script signs for the project's deployment host
(`https://graphs.alintech.com.au`) **plus** `http://localhost:3000`, so the same
build also loads during local testing. See [`docs/signing.md`](./signing.md) for
the full mechanism.

> **A private signature is valid only for the Grafana base URL(s) it was signed
> for.** If the `root_url` your Grafana actually serves from does not match one of
> the signed `rootUrls`, the plugin **will not load** (signature-invalid). This is
> the most common private-signing failure. If your deployment URL differs from the
> one the published release was signed for, you must **re-sign for your own
> instance URL** — see [`docs/signing.md`](./signing.md#private-signing-for-specific-instances-path-1)
> (`npx @grafana/sign-plugin@latest --rootUrls https://grafana.example.com`). The signed
> `rootUrls` must match Grafana's configured `root_url` exactly (scheme + host +
> any sub-path), so get `[server] root_url` right first.

### Unsigned (allow-listed)

Alternatively, run the plugin **unsigned** by explicitly allow-listing its ids.
This skips the `rootUrls` constraint but tells Grafana to load code it has not
verified. Use it only for testing on a trusted network; production installs
should use a signed build. Set:

```ini
# grafana.ini — [plugins]
allow_loading_unsigned_plugins = wilsonwaters-webview-app,wilsonwaters-webview-panel
```

or the equivalent environment variable:

```
GF_PLUGINS_ALLOW_LOADING_UNSIGNED_PLUGINS=wilsonwaters-webview-app,wilsonwaters-webview-panel
```

**List both ids.** The app and its nested panel register separately; omitting
`wilsonwaters-webview-panel` leaves the panel unavailable. (This is exactly what
the project's dev `docker-compose.yaml` does.)

> Do **not** combine approaches needlessly: if you ship the privately-signed build
> to a host whose URL matches the signed `rootUrls`, you do not need the unsigned
> allow-list at all.

---

## 3. Install methods

All methods end with the plugin living in Grafana's **plugins directory** (by
default `/var/lib/grafana/plugins`, configurable via `[paths] plugins` /
`GF_PATHS_PLUGINS`), followed by a **Grafana restart**.

### 3.1 Bare-metal / VM (download the signed release)

1. Download the packaged, signed archive from the plugin's **GitHub release**
   (`wilsonwaters/grafana-webview-app` → Releases). The release workflow attaches
   the signed `.zip` (and a build-provenance attestation).
2. Unzip it into the Grafana plugins directory so the plugin lands in its own
   folder, e.g. `/var/lib/grafana/plugins/wilsonwaters-webview-app/`:

   ```bash
   sudo unzip wilsonwaters-webview-app-<version>.zip -d /var/lib/grafana/plugins/
   ```

3. Ensure the unpacked `plugin.json`, the frontend assets, **and** the backend
   executable (`gpx_webview...`) are present and readable/executable by the
   Grafana user.
4. If the release was **not** signed for your Grafana's `root_url`, either re-sign
   for your instance (see [§2](#2-signed-vs-unsigned-and-the-rooturls-requirement)
   and [`docs/signing.md`](./signing.md)) or allow unsigned loading of **both**
   ids.
5. **Restart Grafana.**
6. Enable the app: **Administration → Plugins and data → Plugins → Web View →
   Enable**.
7. Complete the **required allowlist step** in [§4](#4-required-first-time-step-allowlist-at-least-one-domain).

### 3.2 Docker

Mount the unpacked plugin into the plugins directory and satisfy the signature
requirement via environment variables.

**Unsigned (allow-listed) example:**

```bash
docker run -d --name grafana \
  -p 3000:3000 \
  -v /path/to/wilsonwaters-webview-app:/var/lib/grafana/plugins/wilsonwaters-webview-app \
  -e GF_PLUGINS_ALLOW_LOADING_UNSIGNED_PLUGINS=wilsonwaters-webview-app,wilsonwaters-webview-panel \
  grafana/grafana:12.4.0
```

**Privately-signed example:** omit the unsigned allow-list, mount a build whose
signed `rootUrls` include your Grafana URL, and make sure the container's
`root_url` matches:

```bash
docker run -d --name grafana \
  -p 3000:3000 \
  -v /path/to/wilsonwaters-webview-app:/var/lib/grafana/plugins/wilsonwaters-webview-app \
  -e GF_SERVER_ROOT_URL=https://grafana.example.com \
  grafana/grafana:12.4.0
```

Notes:

- Mount the **unpacked** plugin folder (containing `plugin.json` + the backend
  binary), not the zip.
- The mounted backend binary must be executable and match the container's
  OS/architecture (Linux/amd64 for the standard Grafana images).
- A `root_url` that does not match the signed `rootUrls` will fail to load — use
  the unsigned allow-list, or re-sign, as in [§2](#2-signed-vs-unsigned-and-the-rooturls-requirement).

### 3.3 Helm / Kubernetes

Using the official `grafana/grafana` Helm chart, deliver the plugin via a mounted
volume (the chart's catalog-oriented `plugins:` list does not apply to a
self-hosted, non-catalog plugin) and set the signature environment via
`grafana.ini` / `env`.

Sketch of the relevant `values.yaml` keys (adapt to your volume source — a
pre-populated PVC, an init container that unpacks the release zip, etc.):

```yaml
# Mount the unpacked plugin into Grafana's plugins dir.
extraVolumeMounts:
  - name: webview-plugin
    mountPath: /var/lib/grafana/plugins/wilsonwaters-webview-app
    # subPath / existingClaim / hostPath per your storage setup

# Option A — unsigned (allow-list BOTH ids):
grafana.ini:
  plugins:
    allow_loading_unsigned_plugins: wilsonwaters-webview-app,wilsonwaters-webview-panel
  server:
    root_url: https://grafana.example.com

# Option B — privately-signed: drop allow_loading_unsigned_plugins and ensure
# server.root_url matches one of the signed rootUrls.
```

As with Docker, the backend binary must match the node architecture, and
`server.root_url` must match the signed `rootUrls` when running signed. After the
pods roll, enable the app and complete [§4](#4-required-first-time-step-allowlist-at-least-one-domain).

### 3.4 Build from source

For a platform the published release does not cover, or to run your own signed
build:

```bash
# Prerequisites: Node >= 22, Go 1.27.2 (per go.mod), Mage — see the Developer guide.
git clone https://github.com/wilsonwaters/grafana-webview-app.git
cd grafana-webview-app
npm install
npm run build            # frontend → dist/
mage -v build:linux      # backend binary → dist/ (use the matching mage target for your OS)
```

This produces a `dist/` directory containing the full plugin (frontend assets +
`plugin.json` + backend binary). Then either:

- **sign it** for your instance and install the resulting archive —
  `npx @grafana/sign-plugin@latest --rootUrls https://grafana.example.com` (see
  [`docs/signing.md`](./signing.md#private-signing-for-specific-instances-path-1)), or
- copy `dist/` into the plugins directory as
  `.../plugins/wilsonwaters-webview-app/` and run **unsigned** with both ids
  allow-listed (testing on a trusted network only).

Restart Grafana, enable the app, and complete [§4](#4-required-first-time-step-allowlist-at-least-one-domain).
See the [Developer guide](./developer-guide.md) for the full local workflow (the
`npm run server` Docker-Compose dev stack builds and loads the plugin for you —
for local development only, see [§8](#8-do-not-use-the-development-setup-in-production)).

---

## 4. Required first-time step: allowlist at least one domain

**The plugin proxies nothing until you allowlist domains.** On a fresh install the
proxy domain allowlist (`jsonData.allowedDomains`) is **empty**, and an empty
allowlist **denies every proxy request with HTTP 403** by design (fail-closed
SSRF/open-proxy containment). Direct mode is unaffected, but **proxy mode and
"Test URL" will 403 until you add at least one domain.**

As an organisation Admin, open **Administration → Plugins and data → Plugins →
Webview → Configuration**, click **Add domain**, enter the host name, and click
**Save settings**. The backend uses the new allowlist for the next request; you
do not need to restart Grafana. The same page sets the six global limits. See the
[Administration guide §2](./administration.md#2-allowlist-management) for the
per-domain options, the settings API, and who can use the proxy.

> **Provisioning is for development and CI only.** The repository's
> `provisioning/plugins/apps.yaml` allowlists `example.com` for the development
> container and the tests. Do not copy it to a production Grafana: a production
> install starts with an empty allowlist. A provisioning file is applied at every
> Grafana start and replaces the settings saved on the Configuration page.

---

## 5. Verify the install

1. **App loads and is enabled.** Administration → Plugins → Web View shows the app
   enabled with no signature error. (A signature error here usually means a
   `rootUrls` mismatch or a missing unsigned allow-list — see
   [§2](#2-signed-vs-unsigned-and-the-rooturls-requirement) and the
   [Troubleshooting guide](./troubleshooting.md).)
2. **Panel is available.** You can add a **Web View** panel to a dashboard. (If the
   panel is missing while the app loads, you likely did not allow-list
   `wilsonwaters-webview-panel`.)
3. **Backend is up (for proxy mode).** The panel editor offers Proxy mode and
   "Test URL" (these are disabled when no backend is detected via `/health`).
4. **Allowlist took effect.** As an Editor or Admin, run the panel's **Test URL**
   against an **allowlisted** host and confirm you get a verdict (Direct or
   Proxied) rather than a **403** allowlist denial. See
   [Administration guide §1 step 5](./administration.md#1-first-time-setup-checklist).
5. **Proxy mode renders.** Set a panel to **Proxy** for an allowlisted page and
   confirm the page appears in the panel. If it appears without styles or images,
   see the [Troubleshooting guide](./troubleshooting.md#proxy-mode-page-shows-without-styles-images-or-scripts).

---

## 6. Restart requirements

- **Installing, upgrading, or changing `plugin.json` requires a Grafana server
  restart.** Backend app plugins are loaded at startup, and any change to the
  plugin manifest only takes effect after restarting Grafana.
- **Settings changes do not need a restart.** Settings saved on the
  Configuration page (or through the settings API) apply to the next request.
  A provisioning file is the exception: Grafana reads it only at startup. See
  [Administration guide §2](./administration.md#2-allowlist-management) and
  [§10 (upgrade & rollback)](./administration.md#10-upgrade--rollback).

---

## 7. Path 2 (Grafana Cloud catalog) — not yet available

Installing from the Grafana plugin catalog (catalog browse / `grafana-cli plugins
install`) requires the plugin to be **accepted into the catalog with a Community
signature**, which needs Grafana Labs' review. That path (**Path 2**) is **out of
scope for v1**. Until then:

- Use the self-hosted methods in [§3](#3-install-methods).
- A public/community signature is **rejected (HTTP 409)** until the plugin is in
  the catalog.
- Catalog submission prep is tracked separately (see
  [`docs/signing.md`](./signing.md#community-signing--catalog-submission-path-2)).

---

## 8. Do not use the development setup in production

The repository's `docker-compose.yaml` (`npm run server`) is for local
development only. It:

- signs every visitor in as an anonymous **Admin** (`GF_AUTH_ANONYMOUS_ENABLED`
  with `GF_AUTH_ANONYMOUS_ORG_ROLE=Admin`);
- runs Grafana in development mode;
- loads the unsigned plugin (`GF_PLUGINS_ALLOW_LOADING_UNSIGNED_PLUGINS`);
- provisions `example.com` into the allowlist.

Never expose that container to an untrusted network, and never copy its
environment variables or `provisioning/` files to a production Grafana. In
production, install the signed release archive
([§3.1](#31-bare-metal--vm-download-the-signed-release)), keep anonymous access
off unless you accept that anonymous viewers can use the proxy for allowlisted
sites, and start from an empty allowlist.
