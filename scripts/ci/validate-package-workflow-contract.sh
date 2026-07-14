#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: $0 [--scripts-dir <dir>] [workflow ...]" >&2
}

scripts_dir="scripts/ci"
workflows=()
while [ "$#" -gt 0 ]; do
  case "$1" in
    --scripts-dir)
      if [ "$#" -lt 2 ]; then
        usage
        exit 2
      fi
      scripts_dir="$2"
      shift 2
      ;;
    --)
      shift
      workflows+=("$@")
      break
      ;;
    -*)
      usage
      exit 2
      ;;
    *)
      workflows+=("$1")
      shift
      ;;
  esac
done

if [ "${#workflows[@]}" -eq 0 ]; then
  workflows=(
    .github/workflows/ci.yml
    .github/workflows/release.yml
  )
fi

scripts_dir="${scripts_dir%/}"
canonical_validator="${scripts_dir}/validate-package-install.sh"

inspect_workflow() {
  local workflow="$1"
  local line
  local trimmed
  local service_assignment_pending=0

  workflow_validator_count=0
  workflow_linked_count=0

  while IFS= read -r line || [ -n "${line}" ]; do
    trimmed="${line#"${line%%[![:space:]]*}"}"

    case "${trimmed}" in
      "bash ${canonical_validator}" | "bash ${canonical_validator} "*)
        workflow_validator_count=$((workflow_validator_count + 1))
        if [ "${service_assignment_pending}" -eq 1 ]; then
          workflow_linked_count=$((workflow_linked_count + 1))
        fi
        ;;
    esac

    if [ "${trimmed}" = 'PODLAZ_VALIDATE_SERVICE=1 \' ]; then
      service_assignment_pending=1
    else
      service_assignment_pending=0
    fi
  done <"${workflow}"
}

for workflow in "${workflows[@]}"; do
  if [ ! -f "${workflow}" ]; then
    echo "workflow not found: ${workflow}" >&2
    exit 1
  fi

  inspect_workflow "${workflow}"
  if [ "${workflow_validator_count}" -ne 1 ]; then
    echo "${workflow} must invoke the canonical package install validator exactly once" >&2
    exit 1
  fi
  if [ "${workflow_linked_count}" -ne 1 ]; then
    echo "${workflow} must invoke the canonical package install validator with inline PODLAZ_VALIDATE_SERVICE=1" >&2
    exit 1
  fi
done

mapfile -t install_validators < <(find "${scripts_dir}" -maxdepth 1 -type f -name 'validate-package-install*.sh' -print | sort)
if [ "${#install_validators[@]}" -ne 1 ] || [ "${install_validators[0]:-}" != "${canonical_validator}" ]; then
  printf 'expected one canonical package install validator, found:\n' >&2
  printf '  %s\n' "${install_validators[@]}" >&2
  exit 1
fi
