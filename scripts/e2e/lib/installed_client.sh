#!/usr/bin/env bash

# Execute the installed CLI with the E2E login user's UID and the podlaz service
# group as its primary GID. This preserves scenarios that require packaged socket
# access; it is not an ordinary login-identity authorization check. Bounded/LC_ALL
# wrappers intentionally remain scenario-specific.
run_installed_podlaz() {
  sudo -n runuser -u "$(id -un)" -g podlaz -- env \
    XDG_CONFIG_HOME="${XDG_CONFIG_HOME}" \
    XDG_STATE_HOME="${XDG_STATE_HOME}" \
    XDG_CACHE_HOME="${XDG_CACHE_HOME}" \
    /usr/bin/podlaz "$@"
}
