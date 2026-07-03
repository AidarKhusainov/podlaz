#!/usr/bin/env bash
set -euo pipefail

actionlint

shellcheck \
  scripts/build-deb.sh \
  scripts/ci/*.sh \
  packaging/debian/preinstall.sh \
  packaging/debian/postinstall \
  packaging/debian/preremove \
  packaging/debian/postremove
