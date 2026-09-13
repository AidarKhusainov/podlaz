#!/usr/bin/env bash
set -Eeuo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
printf '%s\n' '__RELEASE_LAPTOP_B64_BEGIN__'
base64 -w0 "$ROOT/scripts/acceptance/release-laptop.sh"
printf '\n%s\n' '__RELEASE_LAPTOP_B64_END__'
exit 1
