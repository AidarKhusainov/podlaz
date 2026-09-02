#!/usr/bin/env bash

# Execute the installed CLI as the normal E2E login identity with the exact
# bounded timeout/locale contract shared by package acceptance scenarios.
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
