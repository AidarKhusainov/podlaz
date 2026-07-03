#!/usr/bin/env bash
set -euo pipefail

: "${VERSION:?VERSION is required}"

create_binary_archive() {
  local arch="$1"
  local root_dir="$2"
  local archive_name="podlaz_${VERSION}_linux_${arch}.tar.gz"
  local archive_root="podlaz_${VERSION}_linux_${arch}"
  local stage_parent="/tmp/podlaz-release-${arch}"
  local stage="${stage_parent}/${archive_root}"

  rm -rf "${stage_parent}"
  mkdir -p "${stage}/bin"
  install -m 0755 "${root_dir}/usr/bin/podlaz" "${stage}/bin/podlaz"
  install -m 0755 "${root_dir}/usr/bin/podlazd" "${stage}/bin/podlazd"
  ln -s podlaz "${stage}/bin/plz"
  install -m 0644 README.md LICENSE "${stage}/"

  tar \
    --sort=name \
    --mtime='UTC 2026-01-01' \
    --owner=0 \
    --group=0 \
    --numeric-owner \
    -C "${stage_parent}" \
    -czf "dist/release/${archive_name}" \
    "${archive_root}"
}

mkdir -p dist/release
cp "dist/podlaz_${VERSION}_linux_amd64.deb" dist/release/
cp "dist-arm64/podlaz_${VERSION}_linux_arm64.deb" dist/release/

create_binary_archive amd64 dist/package-root
create_binary_archive arm64 dist-arm64/package-root

(
  cd dist/release
  artifacts=(
    "podlaz_${VERSION}_linux_amd64.tar.gz"
    "podlaz_${VERSION}_linux_arm64.tar.gz"
    "podlaz_${VERSION}_linux_amd64.deb"
    "podlaz_${VERSION}_linux_arm64.deb"
  )
  sha256sum "${artifacts[@]}" > SHA256SUMS
)
cat dist/release/SHA256SUMS
! grep -R -E 'tunwarden|tunwardend|TunWarden' dist/release
