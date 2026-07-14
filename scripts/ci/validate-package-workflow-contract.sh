#!/usr/bin/env bash
set -euo pipefail

workflows=(
  .github/workflows/ci.yml
  .github/workflows/release.yml
)

for workflow in "${workflows[@]}"; do
  if [ "$(grep -Fc 'bash scripts/ci/validate-package-install.sh' "${workflow}")" -ne 1 ]; then
    echo "${workflow} must invoke the canonical package install validator exactly once" >&2
    exit 1
  fi
  if [ "$(grep -Fc "PODLAZ_VALIDATE_SERVICE: '1'" "${workflow}")" -ne 1 ]; then
    echo "${workflow} must enable packaged service validation" >&2
    exit 1
  fi
done

if grep -RFn --include='*.yml' --include='*.yaml' 'validate-package-install-v2.sh' .github/workflows; then
  echo "workflows must not use a second package install validator" >&2
  exit 1
fi
