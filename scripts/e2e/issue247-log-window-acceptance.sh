#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

require_cmd date git grep mktemp python3 runuser sleep sudo systemctl timeout

EVIDENCE_FILE="${E2E_ARTIFACT_DIR}/issue247-log-window-acceptance.txt"

write_evidence() {
  local key="$1" value="$2"
  case "${key}${value}" in
    *$'\n'*|*$'\r'*) fail "invalid normalized issue 247 log-window evidence" ;;
  esac
  printf '%s=%s\n' "${key}" "${value}" >>"${EVIDENCE_FILE}"
}

run_installed_podlaz_bounded() {
  local timeout_seconds="$1"
  shift
  timeout --signal=TERM --kill-after=5s "${timeout_seconds}" \
    sudo -n runuser -u "$(id -un)" -g podlaz -- env \
      LC_ALL=C \
      XDG_CONFIG_HOME="${XDG_CONFIG_HOME}" \
      XDG_STATE_HOME="${XDG_STATE_HOME}" \
      XDG_CACHE_HOME="${XDG_CACHE_HOME}" \
      /usr/bin/podlaz "$@"
}

verify_package_provenance() {
  local build_commit version_output
  build_commit="${GITHUB_SHA:-$(git rev-parse HEAD)}"
  version_output="$(mktemp "${E2E_TMP_ROOT}/issue247-log-window-version.XXXXXX")"
  /usr/bin/podlaz version >"${version_output}" 2>/dev/null || fail "issue 247 log-window installed CLI version failed"
  grep -F -- "${build_commit}" "${version_output}" >/dev/null || fail "issue 247 log-window installed CLI does not identify the tested commit"
  rm -f -- "${version_output}"
  systemctl is-active --quiet podlazd.service || fail "issue 247 log-window acceptance requires the packaged podlazd service"
  write_evidence package_provenance pass
}

assert_visible_marker_and_bounded_window() {
  local broad_output broad_error short_output short_error invocation_start
  broad_output="$(mktemp "${E2E_TMP_ROOT}/issue247-log-window-broad.stdout.XXXXXX")"
  broad_error="$(mktemp "${E2E_TMP_ROOT}/issue247-log-window-broad.stderr.XXXXXX")"
  short_output="$(mktemp "${E2E_TMP_ROOT}/issue247-log-window-short.stdout.XXXXXX")"
  short_error="$(mktemp "${E2E_TMP_ROOT}/issue247-log-window-short.stderr.XXXXXX")"

  # Create a marker that the installed CLI itself must be able to read. This
  # closes the empty-output loophole: lack of journal visibility is not accepted
  # as evidence that the requested lookback was enforced.
  for _ in 1 2 3; do
    run_installed_podlaz_bounded 10s status >/dev/null 2>&1 || fail "issue 247 could not create the old daemon journal marker"
  done
  sudo -n journalctl --sync
  run_installed_podlaz_bounded 20s logs --daemon --since 30s >"${broad_output}" 2>"${broad_error}" || \
    fail "installed podlaz logs could not read the broad daemon journal window"
  grep -Fx 'podlaz daemon logs' "${broad_output}" >/dev/null || fail "broad daemon log window did not render its stable header"
  grep -F 'status request' "${broad_output}" >/dev/null || \
    fail "installed podlaz logs could not observe the generated daemon journal marker"
  write_evidence broad_window_marker_visible pass

  # Move the old marker outside the requested short window, then create a fresh
  # marker that must still be visible. Ignoring --since would return both and the
  # timestamp gate below would fail on the old entry.
  sleep 8
  invocation_start="$(date +%s)"
  run_installed_podlaz_bounded 10s status >/dev/null 2>&1 || fail "issue 247 could not create the fresh daemon journal marker"
  sudo -n journalctl --sync
  run_installed_podlaz_bounded 20s logs --daemon --since 5s >"${short_output}" 2>"${short_error}" || \
    fail "installed podlaz logs --daemon --since 5s failed"
  grep -Fx 'podlaz daemon logs' "${short_output}" >/dev/null || fail "short daemon log window did not render its stable header"
  grep -F 'status request' "${short_output}" >/dev/null || \
    fail "short daemon log window did not contain the fresh visible marker"

  if ! python3 - "${short_output}" "${invocation_start}" <<'PY'
import datetime as dt
import re
import sys
import time

path = sys.argv[1]
start = float(sys.argv[2])
# Requested lookback is 5s. journalctl short timestamps have one-second
# resolution; allow 2s scheduling/formatting slack without admitting the marker
# deliberately aged for 8s before the fresh request.
threshold = start - 7.0
pattern = re.compile(r"^(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\s+(\d{1,2})\s+(\d{2}):(\d{2}):(\d{2})\s")
months = {name: index for index, name in enumerate(("Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"), 1)}
now = dt.datetime.fromtimestamp(start)
seen_journal_line = False

with open(path, encoding="utf-8") as handle:
    for raw in handle:
        match = pattern.match(raw)
        if match is None:
            continue
        seen_journal_line = True
        month, day, hour, minute, second = match.groups()
        candidates = []
        for year in (now.year - 1, now.year, now.year + 1):
            try:
                value = dt.datetime(year, months[month], int(day), int(hour), int(minute), int(second))
            except ValueError:
                continue
            candidates.append(time.mktime(value.timetuple()))
        if not candidates:
            raise SystemExit("could not interpret journal timestamp")
        timestamp = min(candidates, key=lambda candidate: abs(candidate - start))
        if timestamp < threshold:
            raise SystemExit(f"journal line predates bounded lookback: {raw.strip()}")

if not seen_journal_line:
    raise SystemExit("short lookback contained no timestamped journal lines")
PY
  then
    rm -f -- "${broad_output}" "${broad_error}" "${short_output}" "${short_error}"
    fail "installed podlaz logs did not prove the requested lookback against visible journal entries"
  fi

  rm -f -- "${broad_output}" "${broad_error}" "${short_output}" "${short_error}"
  write_evidence short_window_excludes_old_visible_marker pass
}

: >"${EVIDENCE_FILE}"
setup_isolated_xdg issue247-log-window-acceptance
verify_package_provenance
assert_visible_marker_and_bounded_window
write_evidence acceptance pass
