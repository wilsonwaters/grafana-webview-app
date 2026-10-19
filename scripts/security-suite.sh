#!/usr/bin/env bash
#
# Non-skippable security suite guard (TC5, AC 36).
#
# AC 36: "All security tests pass in CI on every PR (security suite cannot be
# skipped)." This script makes the mandatory backend security suite impossible
# to silently skip:
#
#   1. It FAILS if the backend is absent (no Magefile.go), instead of treating a
#      missing backend as "nothing to test" (which is what the main build job's
#      `if: has-backend == 'true'` gating would do).
#   2. It runs the security Go tests explicitly with `-count=1` (no cache) and
#      captures the output.
#   3. It asserts a `--- PASS:` line exists for EVERY required AC test function
#      below. A deleted, renamed, skipped, or failing AC test therefore FAILS
#      CI, closing the gap where `go test -run <regexp>` exits 0 when its regexp
#      matches nothing.
#
# REQUIRED_TESTS is the single source of truth for the mandatory security
# coverage. When a new security AC test is added, add its function name here so
# the gate is updated deliberately rather than by accident.
#
# NOTE: This workflow runs the suite. Making the `security-suite` CI job a
# *required* status check (so a PR cannot merge while it is red or absent) is a
# repository branch-protection setting and must be configured by a repo admin;
# it cannot be enforced from the workflow file alone.

set -euo pipefail

# Run from the repository root regardless of where the script is invoked.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_ROOT}"

# --- Required AC test functions (single source of truth) ---------------------
# Format: "<label>:TestFunctionName" (the label must not contain ':'). Each
# entry must produce a `--- PASS:` line.
# AC -> test mapping for the backend security suite (AC 17-29, 31):
#   TC1: AC 17-22 in pkg/plugin/proxy_security_ssrf_test.go
#   TC2: AC 23-29 in pkg/plugin/proxy_security_limits_test.go
#   AC 19 opt-in: pkg/plugin/proxy_ac19_optin_test.go
#   AC 31 (hide-selector markup injection): pkg/plugin/rewrite_cr5_test.go
# Security-review fixes (VR-n, 2026-06-10 review) and v1.0.0 review fixes
# (RV1-n, RV2) follow the AC entries.
REQUIRED_TESTS=(
  # TC1 - SSRF / allowlist / blocklist / schemes (AC 17-22)
  "AC17:TestTC1AC17FreshInstallEmptyAllowlistFailsClosed"
  "AC18:TestTC1AC18AllowlistEnforcedOnAllEndpoints"
  "AC19:TestTC1AC19AllowlistedHostResolvingToPrivateIsBlocked"
  "AC20:TestTC1AC20MetadataAndLinkLocalBlockedRegardlessOfAllowlist"
  "AC21:TestTC1AC21DNSRebindingPrevented"
  "AC22:TestTC1AC22NonHTTPSchemesRejected"
  # TC2 - limits / headers / redirects / audit / metrics (AC 23-29)
  "AC23:TestSecurityTC2_AC23_RedirectIntoDeniedDestinationBlocked"
  "AC23:TestSecurityTC2_AC23_RedirectAllowlistedHostRewritten"
  "AC24:TestSecurityTC2_AC24_OversizeResponseRejected413"
  "AC24:TestSecurityTC2_AC24_WithinLimitResponsePasses"
  "AC25:TestSecurityTC2_AC25_PerInstanceRateLimitReturns429"
  "AC25:TestSecurityTC2_AC25_PerInstanceRateLimitMetricReason"
  "AC26:TestSecurityTC2_AC26_OutgoingHeadersStripped"
  "AC27:TestSecurityTC2_AC27_IncomingResponseHeadersStripped"
  "AC28:TestSecurityTC2_AC28_AuditEntryCarriesURLStatusSizeDuration"
  "AC28:TestSecurityTC2_AC28_AuditEntryEmittedOnDenial"
  "AC29:TestSecurityTC2_AC29_AllFourMetricFamiliesExposed"
  "AC29:TestSecurityTC2_AC29_MetricsIncrementOnSuccessAndDenial"
  # AC 19 private-IP opt-in and AC 31 hide-selector markup injection
  "AC19:TestAC19OptInStillBlocksNonRFC1918"
  "AC19:TestAC19ConnectionReuseHazardMitigated"
  "AC31:TestCR5MarkupInjectionAttemptInjectsNoMarkup"
  # Security review fixes
  "VR3:TestCheckFrameableRoleMatrix"
  "VR3:TestCheckFrameableRoleGateRunsFirst"
  "VR5:TestProxyStripsRefreshHeader"
  "VR6:TestProxyGzipBombBounded"
  "VR6:TestProxyUnsupportedContentEncodingRejected"
  "VR7:TestProxyOversizeUndeclaredLengthIsCleanError"
  "VR11:TestProxyBlocksTranslatedInternalAddresses"
  "VR11:TestClassifyIPTranslationPrefixes"
  "VR11:TestClassifyIPPolicyTranslatedPrivateNotRelaxed"
  "CORS:TestProxySendsNoCORSHeaders"
  "CORS:TestCheckFrameableNoCORS"
  "FR5:TestProxyResourceRouteRemoved"
  # v1.0.0 review fixes (RV2)
  "RV2:TestProxyParseFailureFailsClosed"
  "RV2:TestProxyHostileParseRefused413"
  "RV2:TestRewriteSlotsBoundConcurrency"
  "RV1-8:TestUpstreamFailureDetailOnlyInServerLog"
  "RV1-9:TestDomainRateLimitKeyedOnAllowlistEntry"
  "RV1-23:TestAuditRedactsQueryAndUsesContextLogger"
)

# Packages that contain (or may contain) the mandatory security tests.
SECURITY_PACKAGES="./pkg/plugin/... ./pkg/security/..."

# --- Gap 1: the backend must exist -------------------------------------------
if [ ! -f "Magefile.go" ]; then
  echo "FAIL: Magefile.go not found - backend is missing, security suite cannot run." >&2
  echo "      The mandatory security suite MUST execute on every PR." >&2
  exit 1
fi

# Build a single -run regexp that matches exactly the required functions.
run_regexp=""
for entry in "${REQUIRED_TESTS[@]}"; do
  fn="${entry#*:}"
  if [ -z "${run_regexp}" ]; then
    run_regexp="^(${fn}"
  else
    run_regexp="${run_regexp}|${fn}"
  fi
done
run_regexp="${run_regexp})\$"

echo "Running mandatory security suite (${#REQUIRED_TESTS[@]} required tests)..."
echo "  packages: ${SECURITY_PACKAGES}"
echo "  -run:     ${run_regexp}"
echo

# --- Gap 2 (part 1): run with -count=1 (no cache) and capture output ----------
# We must NOT let a non-zero `go test` exit abort the script before we report
# the per-AC summary, so capture the exit status explicitly.
test_output=""
test_status=0
# shellcheck disable=SC2086  # SECURITY_PACKAGES is an intentional word list.
if test_output="$(go test ${SECURITY_PACKAGES} -run "${run_regexp}" -count=1 -v 2>&1)"; then
  test_status=0
else
  test_status=$?
fi

# Echo the raw test output so CI logs show the full detail.
printf '%s\n' "${test_output}"
echo
echo "================ Required AC security-test summary ================"

# --- Gap 2 (part 2): assert a PASS line exists for every required test --------
missing=0
for entry in "${REQUIRED_TESTS[@]}"; do
  ac="${entry%%:*}"
  fn="${entry#*:}"
  # `go test -v` prints "--- PASS: TestName (0.00s)". A skipped test prints
  # "--- SKIP:" and a failure "--- FAIL:", neither of which matches, so any
  # state other than PASS for a required test is treated as missing/failing.
  if printf '%s\n' "${test_output}" | grep -Eq -- "^[[:space:]]*--- PASS: ${fn}\b"; then
    echo "  [PASS] ${ac} ${fn}"
  else
    echo "  [MISS] ${ac} ${fn}  (no '--- PASS:' line - missing, renamed, skipped, or failed)"
    missing=1
  fi
done
echo "=================================================================="

if [ "${test_status}" -ne 0 ]; then
  echo "FAIL: 'go test' exited with status ${test_status}." >&2
  exit 1
fi

if [ "${missing}" -ne 0 ]; then
  echo "FAIL: one or more required security tests did not PASS (see [MISS] above)." >&2
  exit 1
fi

echo "OK: all ${#REQUIRED_TESTS[@]} required security tests passed."
