#!/usr/bin/env bash
set -euo pipefail

workflows=(
  .github/workflows/ci.yml
  .github/workflows/release.yml
)

for workflow in "${workflows[@]}"; do
  validator_count="$(grep -Fc 'bash scripts/ci/validate-package-install.sh' "${workflow}" || true)"
  if [ "${validator_count}" -ne 1 ]; then
    echo "${workflow} must invoke the canonical package install validator exactly once" >&2
    exit 1
  fi

  service_count="$(grep -Fc "PODLAZ_VALIDATE_SERVICE: '1'" "${workflow}" || true)"
  if [ "${service_count}" -ne 1 ]; then
    echo "${workflow} must enable packaged service validation" >&2
    exit 1
  fi
done

mapfile -t install_validators < <(find scripts/ci -maxdepth 1 -type f -name 'validate-package-install*.sh' -print | sort)
if [ "${#install_validators[@]}" -ne 1 ] || [ "${install_validators[0]:-}" != scripts/ci/validate-package-install.sh ]; then
  printf 'expected one canonical package install validator, found:\n' >&2
  printf '  %s\n' "${install_validators[@]}" >&2
  exit 1
fi
