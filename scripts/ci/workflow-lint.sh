#!/usr/bin/env bash
set -euo pipefail

actionlint

mapfile -t shell_scripts < <(
  find \
    scripts/build-deb.sh \
    scripts/ci \
    scripts/e2e \
    packaging/debian \
    -type f \
    \( -name '*.sh' -o -name postinstall -o -name preremove -o -name postremove \) \
    ! -path 'scripts/e2e/lib/*' \
    -print | sort
)

shellcheck -x -s bash -P scripts/e2e "${shell_scripts[@]}"
