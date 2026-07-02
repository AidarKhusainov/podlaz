#!/bin/sh
set -eu

marker_dir="${PODLAZ_MAINTSCRIPT_RUN_DIR:-/run/podlaz-maintscript}"
marker="$marker_dir/repair-helper-enable"

if [ "${1:-}" = "install" ] && [ -n "${2:-}" ]; then
	install -d -m 0700 "$marker_dir" || true
	touch "$marker" || true
fi

exit 0
