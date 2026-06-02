# Plugin Signing & Secrets

This plugin is distributed **signed**. Signing is performed in CI by the release
workflow (`.github/workflows/release.yml`), which reads a Grafana Cloud **Access
Policy token** from the GitHub repository secret `GRAFANA_ACCESS_POLICY_TOKEN`.

This guide explains how to create that token and store it as a secret. It maps to
the two deployment paths in the project brief:

- **Path 1 — Private signing** (self-hosted, available now): a build signed for
  specific Grafana instance URLs (`rootUrls`).
- **Path 2 — Community signing** (Grafana Cloud catalog, later): requires Grafana
  Labs review; uses the same token mechanism.

## Important: keep the token secret

> **Do not paste the Access Policy token into chat, issues, PRs, commits, or any
> file.** It is a credential. The only places it should live are (a) the GitHub
> repository **Secrets** store, and (b) optionally your own shell as an
> environment variable for local signing. If a token is ever exposed, revoke it
> immediately in the Grafana Cloud Access Policies UI and create a new one.

## Step 1 — Grafana Cloud account & org slug

1. Create a free Grafana Cloud account at https://grafana.com/auth/sign-up (the
   free tier is sufficient for signing).
2. Note your **organization slug**. The plugin ID is `wilsonwaters-webview-app`,
   so for catalog (Community) submission the org slug **must** be `wilsonwaters`.
   For private signing the prefix is not strictly enforced, but keeping the org
   slug `wilsonwaters` avoids problems later.

## Step 2 — Create an Access Policy token

1. Go to **https://grafana.com/orgs/&lt;your-org&gt;/access-policies**
   (Grafana Cloud → Administration → Access Policies).
2. **Create access policy**:
   - Realm: your organization
   - Scope: **`plugins:write`** (this is the scope the signing tool needs)
3. **Add a token** to that policy. Copy the token value **once** — it is not
   shown again.

Reference: https://grafana.com/developers/plugin-tools/publish-a-plugin/sign-a-plugin#generate-an-access-policy-token

## Step 3 — Store it as a GitHub repository secret

Add the token as `GRAFANA_ACCESS_POLICY_TOKEN`:

**Via the GitHub web UI (recommended):**

1. Repo → **Settings** → **Secrets and variables** → **Actions**
2. **New repository secret**
3. Name: `GRAFANA_ACCESS_POLICY_TOKEN`
4. Secret: paste the token → **Add secret**

**Via the GitHub CLI (if you have it locally):**

```bash
gh secret set GRAFANA_ACCESS_POLICY_TOKEN --repo wilsonwaters/grafana-webview-app
# paste the token when prompted (it is not echoed)
```

That's the only setup needed for CI signing. On the next `v*` tag push the
release workflow signs and packages the plugin automatically.

## Step 4 — Cut a signed release (CI)

```bash
git tag v0.1.0
git push origin v0.1.0
```

The release workflow builds the frontend + backend, signs using the token, and
attaches the packaged, signed zip to a GitHub release.

## Private signing for specific instances (Path 1)

A **private**-signed build is valid only for the Grafana root URLs you specify.
To sign locally for your own instance:

```bash
export GRAFANA_ACCESS_POLICY_TOKEN=...        # your token; do not commit it
npm run build && mage -v build:linux
npm run sign -- --rootUrls https://grafana.example.com    # your Grafana base URL(s)
```

The signed `MANIFEST.txt` is written into `dist/`. Users install the resulting
zip on a Grafana instance served from one of the `rootUrls`. See
`docs/installation.md` (forthcoming) for install methods.

## Community signing / catalog submission (Path 2)

Submitting to the Grafana plugin catalog for Community signing requires passing
Grafana Labs' review (security, quality, docs). That process — pre-submission
checklist, validator run, and the submission cover letter — is documented in
`docs/publishing.md` (forthcoming, catalog-prep stream). Submission itself is
deferred per the project plan.

## How to hand the token to an automated session

If an automated/orchestrated session needs to sign during development, **do not
paste the token into the conversation**. Instead:

1. Add it to GitHub Secrets (Step 3) and let the **release workflow** sign in CI, or
2. Provide it to the environment out-of-band as the `GRAFANA_ACCESS_POLICY_TOKEN`
   environment variable through the platform's secret-injection mechanism (not
   via chat).
