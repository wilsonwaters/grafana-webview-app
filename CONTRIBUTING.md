# Contributing to Webview

Thank you for considering a contribution to this project. Please take a moment to read these guidelines before opening an issue or pull request.

---

## Code of Conduct

All contributors are expected to follow the [Code of Conduct](CODE_OF_CONDUCT.md). Please report unacceptable behaviour via [GitHub Security Advisories](https://github.com/wilsonwaters/grafana-webview-app/security/advisories/new) or by contacting the maintainers through GitHub.

---

## How to Contribute

### Reporting Bugs and Requesting Features

Use the [issue templates](.github/ISSUE_TEMPLATE/) to open a bug report or feature request. Before opening a new issue, please search existing issues to avoid duplicates.

For security vulnerabilities, do **not** open a public issue — see [SECURITY.md](SECURITY.md).

### Discussions

For questions, ideas, or general discussion, use [GitHub Discussions](https://github.com/wilsonwaters/grafana-webview-app/discussions).

---

## Branch Naming

| Type | Pattern | Example |
|------|---------|---------|
| Feature | `feat/<short-description>` | `feat/proxy-header-rewrite` |
| Bug fix | `fix/<short-description>` | `fix/iframe-csp-fallback` |
| Documentation | `docs/<short-description>` | `docs/installation-guide` |
| Chore / tooling | `chore/<short-description>` | `chore/update-sdk` |
| Release | `release/<version>` | `release/1.2.0` |

Branch names should be lowercase and use hyphens, not underscores.

---

## Commit Messages — Conventional Commits

This project follows [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).

```
<type>(<optional scope>): <short description>

[optional body]

[optional footer(s)]
```

Allowed types: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`, `revert`.

Examples:

```
feat(proxy): add header sanitisation for X-Frame-Options responses
fix(iframe): fall back to direct mode when backend is unavailable
docs: add installation guide stub
```

The PR title must follow this format because it is used as the squash-merge commit message.

---

## Pull Request Process

1. **Fork** the repository and create your branch from `main` using the naming convention above.
2. **Implement** your change, keeping commits focused and atomic.
3. **Ensure all required checks pass locally** before opening a PR (see [Required Checks](#required-checks) below).
4. **Open a PR** against `main` using the [pull request template](.github/PULL_REQUEST_TEMPLATE.md). Fill in every section.
5. **Link the relevant issue** using a closing keyword (`Closes #123`) in the PR description.
6. **Request a review** — at least one maintainer approval is required before merging.
7. **Address review feedback** by pushing additional commits (do not force-push after review has started).
8. Maintainers will squash-merge the PR using the Conventional Commit PR title.

### Required Checks

Run all of the following locally and ensure they pass before opening or updating a PR:

```bash
# Frontend lint
npm run lint

# Frontend unit tests
npm run test:ci

# Frontend production build
npm run build

# Go backend build
mage -v build:linux
```

CI will also run these automatically on every PR. A PR cannot be merged while any required check is failing.

---

## Reviewer Expectations

Reviewers should:

- Check correctness, security implications, and consistency with the existing architecture.
- Verify that tests cover the changed behaviour.
- Ensure the PR title follows Conventional Commits.
- Confirm that no secrets, credentials, or personal data are included.
- Provide constructive, specific feedback and approve only when all concerns are resolved.

---

## Developer Certificate of Origin

By contributing to this project you agree that your contributions are your own work (or you have the right to submit them) and that they may be distributed under the [Apache License 2.0](LICENSE).
