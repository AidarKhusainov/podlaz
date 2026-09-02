#!/usr/bin/env bash

# Execute the installed CLI as the normal E2E login identity while retaining
# only the explicit XDG environment needed by the client. Bounded/LC_ALL and
# privileged execution wrappers intentionally remain scenario-specific.
run_installed_podlaz() {
  sudo -n runuser -u "$(id -un)" -g podlaz -- env \
    XDG_CONFIG_HOME="${XDG_CONFIG_HOME}" \
    XDG_STATE_HOME="${XDG_STATE_HOME}" \
    XDG_CACHE_HOME="${XDG_CACHE_HOME}" \
    /usr/bin/podlaz "$@"
}
