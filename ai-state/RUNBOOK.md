# RUNBOOK — Development Environment

How to build, run, and test the `wilsonwaters-webview-app` plugin locally. These
steps were verified during project setup against Grafana 12.4.0.

## Prerequisites

- **Node.js** >= 22 (`.nvmrc` pins the version)
- **Go** >= 1.24
- **Mage** — `go install github.com/magefile/mage@latest` (ensure `$(go env GOPATH)/bin` is on `PATH`)
- **Docker** + **Docker Compose v2** (the `docker compose` subcommand)
- **Playwright Chromium** (for E2E / runtime verification)

## First-time setup

```bash
npm install                       # frontend dependencies
npx playwright install chromium   # E2E browser (system deps: npx playwright install-deps chromium)
mage -v build:linux               # build the Go backend -> dist/gpx_webview_linux_amd64
npm run build                     # build the frontend -> dist/
```

> The backend binary must match the container architecture. The dev container is
> linux/amd64, so use `mage -v build:linux`. For your host during pure-frontend
> work, `mage -v build:backend` builds for the current OS.

## Run the dev environment

```bash
docker compose up -d --build      # starts Grafana with the plugin mounted from dist/
# Grafana: http://localhost:3000  (anonymous auth is enabled in dev; admin/admin otherwise)
# Delve (backend debugger) is exposed on :2345
```

The compose file mounts `./dist` into the container's plugin directory and sets
`GF_PLUGINS_ALLOW_LOADING_UNSIGNED_PLUGINS=wilsonwaters-webview-app`, so the
unsigned dev build loads.

Verify it loaded:

```bash
curl -s http://localhost:3000/api/health
curl -s http://localhost:3000/api/plugins/wilsonwaters-webview-app/settings
# backend resource calls: http://localhost:3000/api/plugins/wilsonwaters-webview-app/resources/<route>
```

Tear down: `docker compose down`.

## Development workflow

- `npm run dev` — webpack watch (rebuilds frontend on change)
- `mage -v build:linux` — rerun after **every** Go change, then `docker compose restart`
- Frontend changes hot-reload via the livereload plugin; backend changes require
  a rebuild + container restart.

## Tests & checks

```bash
npm run test:ci      # frontend unit tests (Jest)
mage test            # backend unit tests (Go)
npm run e2e          # Playwright E2E (@grafana/plugin-e2e)
npm run lint         # ESLint
npm run typecheck    # tsc --noEmit
```

Validate the packaged plugin before release with the build-plugin skill /
`@grafana/plugin-validator` (see `.claude/skills/validate-plugin/`).

## Headless tester (Playwright) notes

- Browsers may be installed under `/opt/pw-browsers` (check
  `echo $PLAYWRIGHT_BROWSERS_PATH`).
- A quick smoke test: launch Chromium and navigate to
  `http://localhost:3000/login`, screenshot it. This is the mechanism runtime /
  system-verification sub-agents use to act as a human tester.

## Sandbox quirk: Docker daemon

In the ephemeral cloud dev sandbox used for orchestration, the Docker daemon
does **not** persist reliably between steps and may need restarting:

```bash
sudo bash -c 'setsid dockerd >/var/log/dockerd.log 2>&1 < /dev/null &'
# wait a few seconds, then: docker info
```

On a normal local machine (Docker Desktop or a system `dockerd`) this is not an
issue — the daemon runs persistently and `docker compose up` just works.

## Outbound TLS note

In the sandbox, outbound calls to `grafana.com` log TLS verification errors
(`certificate signed by unknown authority`) because of the network proxy. This
only affects Grafana's plugin-catalog/update checks, not the plugin itself, and
does not occur on a normal network.
