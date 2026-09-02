#!/usr/bin/env bash

# Normalized E2E evidence writers. Scenario-specific evidence schemas stay with
# their callers; these helpers only own stable serialization and validation.

append_evidence_kv() {
  local file="$1" key="$2" value="$3"
  case "${key}${value}" in
    *$'\n'*|*$'\r'*) fail "invalid normalized evidence" ;;
  esac
  printf '%s=%s\n' "${key}" "${value}" >>"${file}"
}

append_evidence_pass() {
  local file="$1" key="$2"
  case "${key}" in
    *[!A-Za-z0-9_.-]*) fail "invalid normalized evidence key" ;;
  esac
  printf '%s=pass\n' "${key}" >>"${file}"
}
