#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
guard="${script_dir}/repository-structure.sh"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

new_fixture() {
  local fixture
  fixture="$(mktemp -d)"
  git -C "${fixture}" init -q
  mkdir -p "${fixture}/docs" "${fixture}/internal/app" "${fixture}/scripts/ci" "${fixture}/.github"
  printf '# README\n' > "${fixture}/README.md"
  printf '# Agents\n' > "${fixture}/AGENTS.md"
  printf '# Architecture\n' > "${fixture}/ARCHITECTURE.md"
  printf '# CLI\n' > "${fixture}/docs/cli.md"
  printf 'package app\n' > "${fixture}/internal/app/app.go"
  printf 'MIT\n' > "${fixture}/LICENSE"
  git -C "${fixture}" add .
  printf '%s\n' "${fixture}"
}

expect_pass() {
  local name="$1"
  local fixture="$2"
  shift 2
  local output
  if ! output="$(bash "${guard}" "$@" "${fixture}" 2>&1)"; then
    printf '%s\n' "${output}" >&2
    fail "${name}: expected success"
  fi
  printf 'PASS: %s\n' "${name}"
}

expect_fail() {
  local name="$1"
  local fixture="$2"
  local expected="$3"
  shift 3
  local output
  if output="$(bash "${guard}" "$@" "${fixture}" 2>&1)"; then
    printf '%s\n' "${output}" >&2
    fail "${name}: expected failure"
  fi
  if ! grep -Fq -- "${expected}" <<<"${output}"; then
    printf '%s\n' "${output}" >&2
    fail "${name}: expected diagnostic containing: ${expected}"
  fi
  printf 'PASS: %s\n' "${name}"
}

fixtures=()
cleanup() {
  if ((${#fixtures[@]} > 0)); then
    rm -rf -- "${fixtures[@]}"
  fi
}
trap cleanup EXIT

fixture="$(new_fixture)"; fixtures+=("${fixture}")
expect_pass 'allowed repository' "${fixture}"
expect_pass 'allowed final repository' "${fixture}" --final

fixture="$(new_fixture)"; fixtures+=("${fixture}")
git -C "${fixture}" rm -q -f docs/cli.md
expect_fail 'missing required knowledge surface' "${fixture}" 'required knowledge surface is missing: docs/cli.md'

fixture="$(new_fixture)"; fixtures+=("${fixture}")
printf '# Extra prose\n' > "${fixture}/docs/operations.md"
git -C "${fixture}" add docs/operations.md
expect_fail 'extra permanent prose' "${fixture}" 'permanent prose is not allowed: docs/operations.md'

fixture="$(new_fixture)"; fixtures+=("${fixture}")
printf '#!/usr/bin/env bash\n' > "${fixture}/scripts/ci/issue321-check.sh"
git -C "${fixture}" add scripts/ci/issue321-check.sh
expect_fail 'issue-numbered maintained path' "${fixture}" 'issue-numbered maintained path is not allowed: scripts/ci/issue321-check.sh'

fixture="$(new_fixture)"; fixtures+=("${fixture}")
{
  printf 'package app\n\nfunc Test%s%sRegression() {}\n' 'Issue' '321'
} > "${fixture}/internal/app/app_test.go"
git -C "${fixture}" add internal/app/app_test.go
expect_fail 'TestIssue identifier' "${fixture}" 'issue-numbered test identifier is not allowed:'

fixture="$(new_fixture)"; fixtures+=("${fixture}")
mkdir -p "${fixture}/docs/superpowers/plans"
printf '# Temporary plan\n' > "${fixture}/docs/superpowers/plans/active.md"
git -C "${fixture}" add docs/superpowers/plans/active.md
expect_pass 'active superpowers artifacts' "${fixture}"
expect_fail 'final superpowers artifacts' "${fixture}" 'transient superpowers artifact is not allowed:' --final

fixture="$(new_fixture)"; fixtures+=("${fixture}")
printf '\nSee [retired docs](./state-and-security.md).\n' >> "${fixture}/docs/cli.md"
git -C "${fixture}" add docs/cli.md
expect_fail 'stale retired-doc reference' "${fixture}" 'stale retired-doc reference in docs/cli.md: state-and-security.md'

fixture="$(new_fixture)"; fixtures+=("${fixture}")
mkdir -p "${fixture}/.github/workflows"
cat > "${fixture}/.github/workflows/ci.yml" <<'YAMLEOF'
name: CI
jobs:
  test:
    name: Issue 321 regression
YAMLEOF
git -C "${fixture}" add .github/workflows/ci.yml
expect_fail 'issue-oriented workflow label' "${fixture}" 'issue-oriented workflow label is not allowed:'

fixture="$(new_fixture)"; fixtures+=("${fixture}")
printf '## Checklist\n' > "${fixture}/.github/pull_request_template.md"
mkdir -p "${fixture}/vendor/example"
printf '# Generated vendor notes\n' > "${fixture}/vendor/example/README.md"
printf '// Historical context: Issue #321.\npackage app\n' > "${fixture}/internal/app/app.go"
git -C "${fixture}" add .
expect_pass 'allow-list edge cases' "${fixture}"

ci_workflow="${script_dir}/../../.github/workflows/ci.yml"
if ! grep -Fq -- 'types: [opened, synchronize, reopened, ready_for_review, converted_to_draft]' "${ci_workflow}"; then
  fail 'pull-request CI must rerun repository structure checks when draft state changes'
fi
active_block="$(grep -F -A2 -- '- name: Active repository structure' "${ci_workflow}" || true)"
if ! grep -Fq -- "if: \${{ github.event_name == 'pull_request' && github.event.pull_request.draft }}" <<<"${active_block}" \
  || ! grep -Fq -- 'run: bash scripts/ci/repository-structure.sh' <<<"${active_block}" \
  || grep -Fq -- '--final' <<<"${active_block}"; then
  fail 'draft pull-request CI must allow active repository structure without --final'
fi
final_block="$(grep -F -A2 -- '- name: Final repository structure' "${ci_workflow}" || true)"
if ! grep -Fq -- "if: \${{ github.event_name != 'pull_request' || github.event.pull_request.draft == false }}" <<<"${final_block}" \
  || ! grep -Fq -- 'run: bash scripts/ci/repository-structure.sh --final' <<<"${final_block}"; then
  fail 'merge-ready pull-request CI must enforce repository-structure.sh --final'
fi
printf 'PASS: pull-request CI enforces lifecycle-aware repository structure\n'
