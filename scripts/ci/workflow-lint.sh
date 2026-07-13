#!/usr/bin/env bash
set -euo pipefail

actionlint

mapfile -t core_scripts < <(
  find \
    scripts/build-deb.sh \
    scripts/ci \
    packaging/debian \
    -type f \
    \( -name '*.sh' -o -name postinstall -o -name preremove -o -name postremove \) \
    -print | sort
)

mapfile -t e2e_scripts < <(
  find \
    scripts/e2e \
    -type f \
    -name '*.sh' \
    ! -path 'scripts/e2e/lib/*' \
    -print | sort
)

shellcheck -x -s bash "${core_scripts[@]}"
bash scripts/ci/validate-installed-status-test.sh

# E2E entrypoints intentionally collect host diagnostics through sudo-owned commands
# into user-owned artifact files and carry defensive state variables for cleanup.
shellcheck -x -s bash -P scripts/e2e \
  -e SC2024,SC2034,SC2086,SC2155,SC2318 \
  "${e2e_scripts[@]}"
