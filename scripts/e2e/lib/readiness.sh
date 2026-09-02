#!/usr/bin/env bash

# Shared readiness polling for installed-package E2E scenarios.
# The caller must provide fail() and sudo access where service readiness is used.
# Sourceable library: deliberately does not alter shell options.

E2E_READINESS_POLL_INTERVAL_SECONDS=0.1

_readiness_attempts_for_timeout() {
  local timeout_seconds="$1"
  [[ "${timeout_seconds}" =~ ^[1-9][0-9]*$ ]] || fail "readiness timeout must be a positive whole number of seconds"
  printf '%d\n' "$((timeout_seconds * 10))"
}

wait_for_daemon_socket() {
  local socket_path="$1" timeout_seconds="$2" attempts attempt
  attempts="$(_readiness_attempts_for_timeout "${timeout_seconds}")" || return
  for ((attempt = 0; attempt < attempts; attempt++)); do
    [[ -S "${socket_path}" ]] && return 0
    sleep "${E2E_READINESS_POLL_INTERVAL_SECONDS}"
  done
  fail "daemon socket did not become ready within ${timeout_seconds}s: ${socket_path}"
}

wait_for_service_active() {
  local service_name="$1" timeout_seconds="$2" attempts attempt
  attempts="$(_readiness_attempts_for_timeout "${timeout_seconds}")" || return
  for ((attempt = 0; attempt < attempts; attempt++)); do
    if sudo -n systemctl is-active --quiet "${service_name}"; then
      return 0
    fi
    sleep "${E2E_READINESS_POLL_INTERVAL_SECONDS}"
  done
  fail "service did not become active within ${timeout_seconds}s: ${service_name}"
}

wait_for_daemon_ready() {
  local socket_path="$1" service_name="$2" timeout_seconds="$3" attempts attempt
  attempts="$(_readiness_attempts_for_timeout "${timeout_seconds}")" || return
  for ((attempt = 0; attempt < attempts; attempt++)); do
    if [[ -S "${socket_path}" ]] && sudo -n systemctl is-active --quiet "${service_name}"; then
      return 0
    fi
    sleep "${E2E_READINESS_POLL_INTERVAL_SECONDS}"
  done
  fail "daemon did not become ready within ${timeout_seconds}s: ${service_name} (${socket_path})"
}
