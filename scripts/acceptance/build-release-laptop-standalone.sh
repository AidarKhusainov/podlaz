#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
ENTRYPOINT="$ROOT/release-laptop.sh"
MODULE_DIR="$ROOT/lib/release-laptop"
MODULES=(core.sh product.sh host_exercise.sh lifecycle.sh evidence.sh scenarios.sh legacy.sh)

usage() {
  printf 'usage: %s OUTPUT\n' "${0##*/}" >&2
}

(($# == 1)) || { usage; exit 2; }
output="$1"
[[ ! -L "$output" ]] || { printf 'standalone builder: refusing symlink output: %s\n' "$output" >&2; exit 1; }
[[ -f "$ENTRYPOINT" && ! -L "$ENTRYPOINT" ]] || { printf 'standalone builder: entrypoint missing or unsafe\n' >&2; exit 1; }
[[ -d "$MODULE_DIR" && ! -L "$MODULE_DIR" ]] || { printf 'standalone builder: module directory missing or unsafe\n' >&2; exit 1; }

for module in "${MODULES[@]}"; do
  path="$MODULE_DIR/$module"
  [[ -f "$path" && ! -L "$path" ]] || { printf 'standalone builder: module missing or unsafe: %s\n' "$module" >&2; exit 1; }
done

parent="$(dirname -- "$output")"
mkdir -p -- "$parent"
tmp="$(mktemp "$parent/.release-laptop.standalone.XXXXXX")"
trap 'rm -f -- "$tmp"' EXIT

{
  printf '#!/usr/bin/env bash\n'
  printf 'set -Euo pipefail\n'
  printf 'RA_RELEASE_STANDALONE=1\n\n'
  for module in "${MODULES[@]}"; do
    cat -- "$MODULE_DIR/$module"
    printf '\n'
  done
  tail -n +3 -- "$ENTRYPOINT"
} >"$tmp"

bash -n "$tmp"
chmod 0755 "$tmp"
mv -f -- "$tmp" "$output"
trap - EXIT
printf 'Built standalone release acceptance controller: %s\n' "$output"
