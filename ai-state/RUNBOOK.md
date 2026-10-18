# RUNBOOK — Development Environment

How to build, run, and test the `wilsonwaters-webview-app` plugin locally. These
steps were verified during project setup against Grafana 12.4.0, and again for
v1.0.0 (M1, 2026-10-18) against grafana-enterprise 12.4.13, 13.0.11, 13.1.8,
13.2.4 and nightly.

## Prerequisites

- **Node.js** >= 22 (`.nvmrc` pins the version)
- **Go** 1.27.2 (the `go` directive in `go.mod`). With the default
  `GOTOOLCHAIN=auto`, an older local Go downloads 1.27.2 automatically.
- **Mage** — `go install github.com/magefile/mage@latest` (ensure `$(go env GOPATH)/bin` is on `PATH`)
- **Docker** + **Docker Compose v2** (the `docker compose` subcommand)
- **Playwright Chromium** (for E2E / runtime verification)

Toolchain versions as of v1.0.0 (M1): create-plugin scaffold 7.13.0,
`@grafana/*` 13.2.4, React 19 (the code stays React 18 compatible, because
Grafana 12.4-13.1 run React 18), `@grafana/plugin-e2e` 3.15.x, Playwright 1.64,
Jest 30, grafana-plugin-sdk-go v0.297.0. Supported Grafana: `>=12.4.0`.

## Windows workstation notes

`@grafana/create-plugin` does not support Windows, and two scaffold parts break
on a Windows checkout: `getEntries()` in `.config/bundler/utils.ts` builds a
glob from a backslash path (the build emits no `module.js`), and Jest finds no
tests when the path contains a dot directory (for example `.claude/worktrees`).
Run the Node toolchain in a Linux container (or WSL). Keep `node_modules` in a
named volume so the Linux binaries do not clash with a Windows install:

```powershell
docker run --rm -v "${PWD}:/work" -v "webview-node-modules:/work/node_modules" -w /work node:22 `
  bash -c "npm ci && npm run typecheck && npm run lint && npm run test:ci && npm run build"
```

- `npx @grafana/create-plugin@latest update` also needs Linux. Run it in the
  same kind of container on a clean checkout, then review the diff.
- `go test -race` needs cgo. On Windows run it in `golang:1.27.2`
  (`mage -v buildAll`, `go vet` and `bash scripts/security-suite.sh` work natively).
- `.gitattributes` keeps `*.sh` at LF, so `.config/entrypoint.sh` works in the
  dev container. If an old checkout still has CRLF, delete the file and
  `git checkout -- <file>`.

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

### E2E against a specific Grafana version

CI runs the suite against the versions that `grafana/plugin-actions/e2e-version`
resolves from `grafanaDependency` (12.4.x, 13.0.x, 13.1.x, 13.2.x, nightly). To
run one of them locally the way CI does:

```bash
ANONYMOUS_AUTH_ENABLED=false DEVELOPMENT=false GRAFANA_VERSION=13.2.4 docker compose up -d --build --force-recreate
npx playwright test --retries=2
```

On Windows, run Playwright in the official image and share the Grafana
container's network so `localhost:3000` resolves:

```powershell
docker run --rm --network container:wilsonwaters-webview-app -v "${PWD}:/work" `
  -v "webview-node-modules:/work/node_modules" -w /work --ipc=host `
  mcr.microsoft.com/playwright:v1.64.1-noble npx playwright test --retries=2
```

Use `--force-recreate` between runs. `tests/appConfig.spec.ts` saves the app
settings, which replaces `jsonData` and drops the provisioned allowlist, so the
TC4 success-path test skips itself when it runs after that spec.

### Dependency audit

```bash
npm audit --omit=dev                              # production deps
osv-scanner scan -L package-lock.json             # what the validator checks
osv-scanner scan -L go.mod
```

`go install github.com/google/osv-scanner/v2/cmd/osv-scanner@latest` installs
osv-scanner. The release validator fails on high/critical findings.

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
