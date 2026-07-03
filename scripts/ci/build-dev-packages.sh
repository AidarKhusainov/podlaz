#!/usr/bin/env bash
set -euo pipefail

: "${CI_PODLAZ_COMMIT:=ci-test}"
: "${CI_PODLAZ_BUILT:=Jun 19 2026}"

PODLAZ_COMMIT="${CI_PODLAZ_COMMIT}" \
PODLAZ_BUILT="${CI_PODLAZ_BUILT}" \
  bash scripts/build-deb.sh 2>&1 | tee /tmp/podlaz-build-deb-amd64.txt

dist/package-root/usr/bin/podlaz version | tee /tmp/podlaz-built-version.txt
grep -Fx 'podlaz version 0.0.0~dev' /tmp/podlaz-built-version.txt
grep -Fx "commit: ${CI_PODLAZ_COMMIT}" /tmp/podlaz-built-version.txt
grep -Fx "built: ${CI_PODLAZ_BUILT}" /tmp/podlaz-built-version.txt

CC=aarch64-linux-gnu-gcc \
PODLAZ_DIST_DIR=dist-arm64 \
PODLAZ_DEB_ARCH=arm64 \
PODLAZ_COMMIT="${CI_PODLAZ_COMMIT}" \
PODLAZ_BUILT="${CI_PODLAZ_BUILT}" \
  bash scripts/build-deb.sh 2>&1 | tee /tmp/podlaz-build-deb-arm64.txt
