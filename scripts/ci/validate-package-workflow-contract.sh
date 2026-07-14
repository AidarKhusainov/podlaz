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

if grep -RFn --include='*.yml' --include='*.yaml' 'validate-package-install-v2.sh' .github/workflows; then
  echo "workflows must not use a second package install validator" >&2
  exit 1
fi
