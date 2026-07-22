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
    ! -path 'scripts/e2e/tests/*' \
    -print | sort
)

mapfile -t e2e_test_scripts < <(
  find \
    scripts/e2e/tests \
    -type f \
    -name '*.sh' \
    -print | sort
)

shellcheck -x -s bash "${core_scripts[@]}"
bash scripts/ci/validate-installed-status-test.sh
bash scripts/ci/validate-package-workflow-contract-test.sh
bash scripts/ci/validate-package-workflow-contract.sh
python3 -m py_compile scripts/e2e/*.py scripts/e2e/lib/*.py scripts/e2e/tests/*.py
python3 -m unittest discover -s scripts/e2e/tests -p 'test_*.py'
bash scripts/e2e/tests/test_process_lifecycle.sh
bash scripts/e2e/tests/test_tun_package_cleanup.sh

# E2E scripts use dynamic source paths, source-only test guards, and test-double
# functions that shellcheck cannot resolve statically. Runtime regression tests
# above exercise those paths before linting the complete script set.
shellcheck -x -s bash -P scripts/e2e \
  -e SC1091,SC2024,SC2034,SC2086,SC2155,SC2317,SC2318 \
  "${e2e_scripts[@]}" \
  "${e2e_test_scripts[@]}"
