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
    -print | sort
)

shellcheck "${shell_scripts[@]}"
