#!/usr/bin/env bash
set -euo pipefail

function deb_arch_to_goarch() {
  case "$1" in
    amd64) echo amd64 ;;
    arm64) echo arm64 ;;
    *) return 1 ;;
  esac
}

binary_version="${TUNWARDEN_VERSION:-0.0.0~dev}"
package_version="${TUNWARDEN_DEB_VERSION:-${binary_version}-1}"
arch="${TUNWARDEN_DEB_ARCH:-amd64}"
out_dir="${TUNWARDEN_DIST_DIR:-dist}"
root_dir="${out_dir}/package-root"
config=".nfpm.tunwarden.yaml"
version_package="github.com/AidarKhusainov/tunwarden/internal/app/cli.version"
package="${out_dir}/tunwarden_${package_version}_${arch}.deb"

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

if ! command -v dpkg-deb >/dev/null 2>&1; then
  echo "dpkg-deb is required to validate the generated Debian package" >&2
  exit 2
fi

rm -rf "${root_dir}"
mkdir -p \
  "${root_dir}/usr/bin" \
  "${root_dir}/usr/lib/systemd/system" \
  "${root_dir}/usr/lib/sysusers.d" \
  "${root_dir}/usr/share/bash-completion/completions" \
  "${root_dir}/usr/share/zsh/vendor-completions" \
  "${root_dir}/usr/share/fish/vendor_completions.d" \
  "${root_dir}/usr/share/man/man1" \
  "${root_dir}/usr/share/man/man8" \
  "${root_dir}/usr/share/doc/tunwarden"

ldflags="-s -w -X ${version_package}=${binary_version}"
CGO_ENABLED=1 GOOS=linux GOARCH="${goarch}" go build -trimpath -ldflags "${ldflags}" -o "${root_dir}/usr/bin/tunwarden" ./cmd/tunwarden
CGO_ENABLED=1 GOOS=linux GOARCH="${goarch}" go build -trimpath -ldflags "${ldflags}" -o "${root_dir}/usr/bin/tunwardend" ./cmd/tunwardend

"${root_dir}/usr/bin/tunwarden" completion bash > "${root_dir}/usr/share/bash-completion/completions/tunwarden"
"${root_dir}/usr/bin/tunwarden" completion zsh > "${root_dir}/usr/share/zsh/vendor-completions/_tunwarden"
"${root_dir}/usr/bin/tunwarden" completion fish > "${root_dir}/usr/share/fish/vendor_completions.d/tunwarden.fish"
chmod 0644 \
  "${root_dir}/usr/share/bash-completion/completions/tunwarden" \
  "${root_dir}/usr/share/zsh/vendor-completions/_tunwarden" \
  "${root_dir}/usr/share/fish/vendor_completions.d/tunwarden.fish"

install -m 0644 packaging/systemd/tunwardend.service "${root_dir}/usr/lib/systemd/system/tunwardend.service"
install -m 0644 packaging/sysusers.d/tunwarden.conf "${root_dir}/usr/lib/sysusers.d/tunwarden.conf"
gzip -9n -c docs/man/tunwarden.1 > "${root_dir}/usr/share/man/man1/tunwarden.1.gz"
gzip -9n -c docs/man/tunwardend.8 > "${root_dir}/usr/share/man/man8/tunwardend.8.gz"
install -m 0644 README.md LICENSE "${root_dir}/usr/share/doc/tunwarden/"
install -m 0644 LICENSE "${root_dir}/usr/share/doc/tunwarden/copyright"
printf 'tunwarden (%s) unstable; urgency=medium\n\n  * Local development package build.\n\n -- Aidar Khusainov <19706697+AidarKhusainov@users.noreply.github.com>  Thu, 11 Jun 2026 00:00:00 +0000\n' "${package_version}" | gzip -9n -c > "${root_dir}/usr/share/doc/tunwarden/changelog.Debian.gz"
find docs -type f ! -path 'docs/man/*' -print | while IFS= read -r file; do
  target="${root_dir}/usr/share/doc/tunwarden/${file}"
  mkdir -p "$(dirname "${target}")"
  install -m 0644 "${file}" "${target}"
done

sed \
  -e "s/__VERSION__/${package_version}/g" \
  -e "s/__ARCH__/${arch}/g" \
  packaging/nfpm.yaml > "${config}"

nfpm package --config "${config}" --packager deb --target "${out_dir}"
rm -f "${config}"

mapfile -t packages < <(find "${out_dir}" -maxdepth 1 -type f -name "tunwarden_*_${arch}.deb" -print | sort)
if [ "${#packages[@]}" -ne 1 ]; then
  echo "expected exactly one generated Debian package, found ${#packages[@]}" >&2
  printf '%s\n' "${packages[@]}" >&2
  exit 1
fi

built_package="${packages[0]}"
built_version="$(dpkg-deb --field "${built_package}" Version)"
if [ "${built_version}" != "${package_version}" ]; then
  echo "generated Debian package has wrong Version metadata" >&2
  echo "expected: ${package_version}" >&2
  echo "actual:   ${built_version}" >&2
  echo "file:     ${built_package}" >&2
  exit 1
fi

if [ "${built_package}" != "${package}" ]; then
  mv "${built_package}" "${package}"
fi

echo "built ${package}"
