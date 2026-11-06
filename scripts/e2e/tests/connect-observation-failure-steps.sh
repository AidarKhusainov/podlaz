#!/usr/bin/env bash
set -Eeuo pipefail
root="$(mktemp -d)"
trap 'rm -rf -- "${root}"' EXIT
export E2E_ARTIFACT_DIR="${root}/public" E2E_TMP_ROOT="${root}/tmp"
mkdir -p "${E2E_ARTIFACT_DIR}" "${E2E_TMP_ROOT}"
source "$(dirname "${BASH_SOURCE[0]}")/../hosted-connect-observation.sh"

wait_for_first() { return 1; }
wait_guest_status() { wait_for_first; }
if observation_wait_verified_active; then
  echo "verified-active failure was swallowed" >&2; exit 1
fi
[[ "${FAILURE_STEP}" == observation.transition-verified-active ]] || exit 1

wait_guest_status() { return 0; }
assert_transition_verified_active() { return 1; }
if observation_assert_exact_active_authority A session table; then
  echo "exact-authority failure was swallowed" >&2; exit 1
fi
[[ "${FAILURE_STEP}" == observation.transition-exact-authority ]] || exit 1

# The real teardown must preserve the preexisting failure even when outer
# cleanup and redaction both fail. It publishes those failures separately.
(
  EVIDENCE_KEYS=(outer.cleanup artifact.privacy)
  : >"${REPORT}"
  stop_synthetic_xray_endpoint() { return 1; }
  stop_system_guest() { return 0; }
  cleanup_outer_plumbing() { return 0; }
  assert_public_artifact_privacy() { return 1; }
  validate_report() { return 0; }
  mark_failure product observation.transition-verified-active
  trap teardown_all EXIT
  false
) && { echo "expected deliberate failure" >&2; exit 1; }
grep -Fx 'failure.class=product' "${REPORT}" >/dev/null
grep -Fx 'failure.step=observation.transition-verified-active' "${REPORT}" >/dev/null
grep -Fx 'outer.cleanup=fail' "${REPORT}" >/dev/null
grep -Fx 'artifact.privacy=fail' "${REPORT}" >/dev/null
