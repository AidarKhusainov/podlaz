#!/usr/bin/env bash
set -euo pipefail

# GO-2026-6809 affects Go releases before 1.26.10, and 1.27.0-1.27.2.
# Keep the module's go 1.26.0 language/graph compatibility floor unchanged.
usage() {
  echo "usage: $0 [--binary <compiled-Go-binary>]" >&2
  exit 2
}

case "$#" in
  0)
    version="$(go env GOVERSION)"
    subject="selected compiler"
    ;;
  2)
    [[ "$1" == --binary ]] || usage
    metadata="$(go version -m "$2")" || {
      echo "cannot inspect Go build metadata: $2" >&2
      exit 1
    }
    version="$(awk 'NR == 1 {print $2}' <<< "${metadata}")"
    subject="binary $2"
    ;;
  *)
    usage
    ;;
esac

if [[ "${version}" =~ ^go([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
  major="${BASH_REMATCH[1]}"
  minor="${BASH_REMATCH[2]}"
  patch="${BASH_REMATCH[3]}"
  if (( major > 1 ||
        (major == 1 && (minor > 27 ||
                        (minor == 27 && patch >= 3) ||
                        (minor == 26 && patch >= 10))) )); then
    echo "patched Go toolchain verified: ${version} (${subject})"
    exit 0
  fi
fi

echo "GO-2026-6809: ${subject} ${version:-unknown} requires a patched Go toolchain (>=1.26.10 on Go 1.26, >=1.27.3 on Go 1.27); refusing release candidate" >&2
exit 1
