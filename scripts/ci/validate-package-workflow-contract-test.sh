#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
guard="${script_dir}/validate-package-workflow-contract.sh"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT

fixture_scripts_dir="${tmp_dir}/scripts/ci"
workflow="${tmp_dir}/workflow.yml"
canonical_validator="${fixture_scripts_dir}/validate-package-install.sh"
mkdir -p "${fixture_scripts_dir}"
printf '#!/usr/bin/env bash\n' >"${canonical_validator}"

expect_failure() {
  if "$@"; then
    echo "expected command to fail: $*" >&2
    exit 1
  fi
}

run_guard() {
  bash "${guard}" --scripts-dir "${fixture_scripts_dir}" "${workflow}"
}

cat >"${workflow}" <<EOF
steps:
  - name: package validator
    run: |
      PODLAZ_VALIDATE_SERVICE=1 \\
        bash ${canonical_validator} package.deb
EOF
run_guard

cat >"${workflow}" <<EOF
steps:
  - name: unrelated service setting
    env:
      PODLAZ_VALIDATE_SERVICE: '1'
    run: echo unrelated
  - name: package validator
    run: bash ${canonical_validator} package.deb
EOF
expect_failure run_guard

cat >"${workflow}" <<EOF
steps:
  - name: package validator
    run: bash ${canonical_validator} package.deb
EOF
expect_failure run_guard

cat >"${workflow}" <<EOF
steps:
  - name: duplicate package validator
    run: |
      PODLAZ_VALIDATE_SERVICE=1 \\
        bash ${canonical_validator} first.deb
      PODLAZ_VALIDATE_SERVICE=1 \\
        bash ${canonical_validator} second.deb
EOF
expect_failure run_guard

cat >"${workflow}" <<EOF
steps:
  - name: package validator
    run: |
      PODLAZ_VALIDATE_SERVICE=1 \\
        bash ${canonical_validator} package.deb
EOF
printf '#!/usr/bin/env bash\n' >"${fixture_scripts_dir}/validate-package-install-extra.sh"
expect_failure run_guard
