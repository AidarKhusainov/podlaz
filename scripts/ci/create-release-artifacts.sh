#!/usr/bin/env bash
set -euo pipefail

: "${VERSION:?VERSION is required}"

mkdir -p dist/release
cp "dist/podlaz_${VERSION}_linux_amd64.deb" dist/release/
cp "dist/podlaz_${VERSION}_linux_arm64.deb" dist/release/
(
  cd dist/release
  sha256sum "podlaz_${VERSION}_linux_amd64.deb" "podlaz_${VERSION}_linux_arm64.deb" > SHA256SUMS
)
cat dist/release/SHA256SUMS
! grep -R -E 'tunwarden|tunwardend|TunWarden' dist/release
