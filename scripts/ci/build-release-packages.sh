#!/usr/bin/env bash
set -euo pipefail

: "${VERSION:?VERSION is required}"
: "${COMMIT_SHA:?COMMIT_SHA is required}"
: "${BUILT_DATE:?BUILT_DATE is required}"

PODLAZ_VERSION="${VERSION}" \
PODLAZ_DEB_VERSION="${VERSION}" \
PODLAZ_DEB_ARCH=amd64 \
PODLAZ_COMMIT="${COMMIT_SHA}" \
PODLAZ_BUILT="${BUILT_DATE}" \
  bash scripts/build-deb.sh

CC=aarch64-linux-gnu-gcc \
PODLAZ_DIST_DIR=dist-arm64 \
PODLAZ_VERSION="${VERSION}" \
PODLAZ_DEB_VERSION="${VERSION}" \
PODLAZ_DEB_ARCH=arm64 \
PODLAZ_COMMIT="${COMMIT_SHA}" \
PODLAZ_BUILT="${BUILT_DATE}" \
  bash scripts/build-deb.sh
