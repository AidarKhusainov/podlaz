#!/usr/bin/env bash

# Shared polling cadence for JSON/status predicates that already define their
# own progress/terminal semantics. The predicate is deliberately scenario-owned.
E2E_STATUS_POLL_INTERVAL_SECONDS=0.2

wait_for_status_match() {
  local description="$1" timeout_seconds="$2" predicate="$3" attempts attempt
  shift 3
  [[ "${timeout_seconds}" =~ ^[1-9][0-9]*$ ]] || fail "status timeout must be a positive whole number of seconds"
  attempts="$((timeout_seconds * 5))"
  for ((attempt = 0; attempt < attempts; attempt++)); do
    if "${predicate}" "$@"; then
      return 0
    fi
    sleep "${E2E_STATUS_POLL_INTERVAL_SECONDS}"
  done
  fail "${description} did not converge within ${timeout_seconds}s"
}
