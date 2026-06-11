#!/usr/bin/env bash
set -euo pipefail

function deb_arch_to_goarch() {
  case "$1" in
    amd64) echo amd64 ;;
    arm64) echo arm64 ;;
    *) return 1 ;;
  esac
}

version="${TUNWARDEN_VERSION:-0.0.0~dev}"
arch="${TUNWARDEN_DEB_ARCH:-amd64}"
out_dir="${TUNWARDEN_DIST_DIR:-dist}"
root_dir="${out_dir}/package-root"
config=".nfpm.tunwarden.yaml"

case "${arch}" in
  amd64|arm64) ;;
  *)
    echo "unsupported Debian architecture: ${arch}" >&2
    echo "supported values: amd64, arm64" >&2
    exit 2
    ;;
esac

goarch="$(deb_arch_to_goarch "${arch}")"

if ! command -v go >/dev/null 2>&1; then
  echo "go is required to build TunWarden binaries" >&2
  exit 2
fi

if ! command -v gzip >/dev/null 2>&1; then
  echo "gzip is required to prepare compressed manual pages" >&2
  exit 2
fi

if ! command -v nfpm >/dev/null 2>&1; then
  echo "nfpm is required to build the Debian package" >&2
  echo "install the pinned version from packaging/package-toolchain.env" >&2
  exit 2
fi

rm -rf "${root_dir}"
mkdir -p \
  "${root_dir}/usr/bin" \
  "${root_dir}/usr/lib/systemd/system" \
  "${root_dir}/usr/lib/sysusers.d" \
  "${root_dir}/usr/share/man/man1" \
  "${root_dir}/usr/share/man/man8" \
  "${root_dir}/usr/share/doc/tunwarden"

build_flags=(-trimpath -ldflags "-s -w -X github.com/AidarKhusainov/tunwarden/internal/app/cli.version=${version}")
GOOS=linux GOARCH="${goarch}" go build "${build_flags[@]}" -o "${root_dir}/usr/bin/tunwarden" ./cmd/tunwarden
GOOS=linux GOARCH="${goarch}" go build "${build_flags[@]}" -o "${root_dir}/usr/bin/tunwardend" ./cmd/tunwardend

install -m 0644 packaging/systemd/tunwardend.service "${root_dir}/usr/lib/systemd/system/tunwardend.service"
install -m 0644 packaging/sysusers.d/tunwarden.conf "${root_dir}/usr/lib/sysusers.d/tunwarden.conf"
gzip -n -c docs/man/tunwarden.1 > "${root_dir}/usr/share/man/man1/tunwarden.1.gz"
gzip -n -c docs/man/tunwardend.8 > "${root_dir}/usr/share/man/man8/tunwardend.8.gz"
install -m 0644 README.md LICENSE "${root_dir}/usr/share/doc/tunwarden/"
install -m 0644 LICENSE "${root_dir}/usr/share/doc/tunwarden/copyright"
printf 'tunwarden (%s) unstable; urgency=medium\n\n  * Local development package build.\n\n -- Aidar Khusainov <19706697+AidarKhusainov@users.noreply.github.com>  Thu, 11 Jun 2026 00:00:00 +0000\n' "${version}" | gzip -n -c > "${root_dir}/usr/share/doc/tunwarden/changelog.Debian.gz"
find docs -type f ! -path 'docs/man/*' -print | while IFS= read -r file; do
  target="${root_dir}/usr/share/doc/tunwarden/${file}"
  mkdir -p "$(dirname "${target}")"
  install -m 0644 "${file}" "${target}"
done

sed \
  -e "s/__VERSION__/${version}/g" \
  -e "s/__ARCH__/${arch}/g" \
  packaging/nfpm.yaml > "${config}"

nfpm package --config "${config}" --packager deb --target "${out_dir}"
rm -f "${config}"

package="${out_dir}/tunwarden_${version}_${arch}.deb"
if [ ! -f "${package}" ]; then
  echo "expected package was not produced: ${package}" >&2
  exit 1
fi

echo "built ${package}"
