#!/usr/bin/env bash
set -euo pipefail

if (($# > 1)); then
  printf 'usage: %s [repository-root]\n' "${0##*/}" >&2
  exit 2
fi

if (($# == 1)); then
  repo_root="$1"
else
  repo_root="$(git rev-parse --show-toplevel 2>/dev/null)" || {
    printf 'repository-structure: must run inside a Git worktree or receive repository-root\n' >&2
    exit 2
  }
fi

repo_root="$(cd -- "${repo_root}" && pwd)"
if ! git -C "${repo_root}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  printf 'repository-structure: not a Git worktree: %s\n' "${repo_root}" >&2
  exit 2
fi

mapfile -d '' -t tracked_paths < <(git -C "${repo_root}" ls-files -z)
violations=()

is_generated_or_vendor() {
  case "$1" in
    vendor/*|third_party/*|generated/*|dist/*|build/*)
      return 0
      ;;
  esac
  return 1
}

is_operational_markdown() {
  case "$1" in
    .github/pull_request_template.md)
      return 0
      ;;
  esac
  return 1
}

is_permanent_knowledge_surface() {
  case "$1" in
    README.md|AGENTS.md|ARCHITECTURE.md|docs/cli.md)
      return 0
      ;;
  esac
  return 1
}

is_maintained_source_test_or_script() {
  case "$1" in
    cmd/*|internal/*|pkg/*|scripts/*)
      return 0
      ;;
  esac
  return 1
}

for path in "${tracked_paths[@]}"; do
  if [[ "${path}" == docs/superpowers/* ]]; then
    violations+=("transient superpowers artifact is not allowed: ${path}")
  fi

  if [[ "${path}" == *.md || "${path}" == docs/man/* ]] \
    && ! is_permanent_knowledge_surface "${path}" \
    && ! is_operational_markdown "${path}" \
    && ! is_generated_or_vendor "${path}" \
    && [[ "${path}" != docs/superpowers/* ]]; then
    violations+=("permanent prose is not allowed: ${path}")
  fi

  if is_maintained_source_test_or_script "${path}" \
    && ! is_generated_or_vendor "${path}" \
    && [[ "${path}" =~ [Ii][Ss][Ss][Uu][Ee][0-9]+ ]]; then
    violations+=("issue-numbered maintained path is not allowed: ${path}")
  fi

done

for path in "${tracked_paths[@]}"; do
  if ! is_maintained_source_test_or_script "${path}" || is_generated_or_vendor "${path}"; then
    continue
  fi
  if [[ ! -f "${repo_root}/${path}" ]]; then
    continue
  fi
  while IFS= read -r match; do
    violations+=("issue-numbered test identifier is not allowed: ${path}:${match}")
  done < <(grep -nI -E 'TestIssue[0-9]+' -- "${repo_root}/${path}" 2>/dev/null || true)
done

retired_doc_references=(
  'docs/README.md'
  'state-and-security.md'
  'e2e.md'
  'debian-package.md'
  'release.md'
  'daemon-api.md'
  'e2e-proxy-data-plane.md'
  'packaged-tun-runtime.md'
  'provider-xray-profiles.md'
  'tun-connect-manual-test.md'
  'tun-uplink-revalidation.md'
  'docs/man/'
)
canonical_docs=(README.md AGENTS.md ARCHITECTURE.md docs/cli.md)
for path in "${canonical_docs[@]}"; do
  [[ -f "${repo_root}/${path}" ]] || continue
  for retired in "${retired_doc_references[@]}"; do
    if grep -Fq -- "${retired}" "${repo_root}/${path}"; then
      violations+=("stale retired-doc reference in ${path}: ${retired}")
    fi
  done
done

if ((${#violations[@]} > 0)); then
  printf 'repository structure policy violations:\n' >&2
  printf '  - %s\n' "${violations[@]}" >&2
  exit 1
fi

printf 'repository structure policy: ok\n'
