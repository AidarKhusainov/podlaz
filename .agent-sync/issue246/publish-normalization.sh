#!/usr/bin/env bash
set -euo pipefail

chunk='.agent-sync/issue246/normalization-fix.b64'
if [[ ! -f "${chunk}" ]]; then
  echo 'No staged issue 246 normalization patch remains.'
  exit 0
fi

base64 -d "${chunk}" > "${RUNNER_TEMP}/issue246-normalization.patch.xz"
printf '%s  %s\n' \
  '3e4b22159d921dbd47c338eea1063bce020197c3212b6d0323d768cfcedf9470' \
  "${RUNNER_TEMP}/issue246-normalization.patch.xz" | sha256sum -c -
xz -dc "${RUNNER_TEMP}/issue246-normalization.patch.xz" > "${RUNNER_TEMP}/issue246-normalization.patch"
printf '%s  %s\n' \
  '2578d2e9b177e9ee95745efe9d951d9b60ce57948f3fad72f3a69e98b926e08d' \
  "${RUNNER_TEMP}/issue246-normalization.patch" | sha256sum -c -
git apply --check "${RUNNER_TEMP}/issue246-normalization.patch"
git apply "${RUNNER_TEMP}/issue246-normalization.patch"

python3 -m py_compile scripts/e2e/lib/tun_soak_*.py scripts/e2e/tests/test_tun_soak_*.py
python3 -m unittest \
  scripts.e2e.tests.test_tun_resource_soak_contract \
  scripts.e2e.tests.test_tun_soak_cleanup \
  scripts.e2e.tests.test_tun_soak_health \
  scripts.e2e.tests.test_tun_soak_isolation \
  scripts.e2e.tests.test_tun_soak_metrics \
  scripts.e2e.tests.test_tun_soak_status
bash -n \
  scripts/e2e/lib/tun_soak_health.sh \
  scripts/e2e/lib/tun_soak_cleanup.sh \
  scripts/e2e/tun-resource-soak.sh
git diff --check

find scripts/e2e -type d -name __pycache__ -prune -exec rm -rf {} +
rm -f -- "${chunk}" "$0"
rmdir .agent-sync/issue246 2>/dev/null || true
rmdir .agent-sync 2>/dev/null || true
test -z "$(git diff --name-only -- .github/workflows)"

git add -A
git diff --cached --check
test -z "$(git diff --cached --name-only -- .github/workflows)"
git config user.name 'podlaz issue 246 automation'
git config user.email '41898282+github-actions[bot]@users.noreply.github.com'
git commit -m 'test: reject ambiguous route normalization'
git push origin HEAD:agent/issue-246-tun-resource-soak
