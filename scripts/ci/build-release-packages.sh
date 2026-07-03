#!/usr/bin/env bash
set -euo pipefail

: "${VERSION:?VERSION is required}"
: "${COMMIT_SHA:?COMMIT_SHA is required}"
: "${BUILT_DATE:?BUILT_DATE is required}"

for arch in amd64 arm64; do
  if [ "${arch}" = arm64 ]; then
    export CC=aarch64-linux-gnu-gcc
  else
    unset CC
  fi

  PODLAZ_VERSION="${VERSION}" \
  PODLAZ_DEB_VERSION="${VERSION}" \
  PODLAZ_DEB_ARCH="${arch}" \
  PODLAZ_COMMIT="${COMMIT_SHA}" \
  PODLAZ_BUILT="${BUILT_DATE}" \
    bash scripts/build-deb.sh

done
