#!/usr/bin/env bash
set -euo pipefail

function deb_arch_to_goarch() {
  case "$1" in
    amd64) echo amd64 ;;
    arm64) echo arm64 ;;
    *) return 1 ;;
  esac
}

function quote_go_ldflag_assignment() {
  local assignment="$1"
  if [[ "${assignment}" == *"'"* ]]; then
    echo "Go ldflag assignment contains an unsupported single quote: ${assignment}" >&2
    exit 2
  fi
  printf "'%s'" "${assignment}"
}

function generate_completion() {
  local shell="$1"
  local target="$2"
  env -u GOOS -u GOARCH -u CC -u CXX CGO_ENABLED=1 go run ./cmd/podlaz completion "${shell}" > "${target}"
}

legacy_tun_helper="tun""2socks"
legacy_tun_helper_upper="TUN""2SOCKS"
legacy_tun_socks_tag="podlaz-""tun-socks"
legacy_tun_adapter_symbol="tun""Adapter"
legacy_tun_adapter_phrase="TUN ""adapter"
readonly legacy_tun_helper legacy_tun_helper_upper legacy_tun_socks_tag legacy_tun_adapter_symbol legacy_tun_adapter_phrase
readonly -a obsolete_tun_artifact_tokens=(
  "${legacy_tun_helper}"
  "${legacy_tun_helper}-LICENSE"
  "PODLAZ_${legacy_tun_helper_upper}_PATH"
  "${legacy_tun_helper_upper}_VERSION"
  "${legacy_tun_helper_upper}_MODULE"
  "${legacy_tun_socks_tag}"
  "${legacy_tun_adapter_symbol}"
  "${legacy_tun_adapter_phrase}"
)

assert_no_obsolete_tun_artifacts() {
  local root="$1"
  local label="$2"
  local failures=0
  local matches=()
  local token file plain_matches

  mapfile -t matches < <(find "${root}" -iname "*${legacy_tun_helper}*" -print)
  if [ "${#matches[@]}" -gt 0 ]; then
    echo "obsolete TUN helper path found in ${label}:" >&2
    printf '%s\n' "${matches[@]}" >&2
    failures=1
  fi

  plain_matches="$(mktemp)"
  for token in "${obsolete_tun_artifact_tokens[@]}"; do
    while IFS= read -r -d '' file; do
      case "${file}" in
        *.gz)
          if gzip -cd -- "${file}" 2>/dev/null | grep -F -- "${token}" >/dev/null; then
            echo "${file}: compressed file contains obsolete TUN helper reference \"${token}\" in ${label}" >&2
            failures=1
          fi
          ;;
        *)
          if grep -I -n -F -- "${token}" "${file}" >"${plain_matches}"; then
            echo "${file}: obsolete TUN helper reference \"${token}\" found in ${label}:" >&2
            cat "${plain_matches}" >&2
            failures=1
          fi
          ;;
      esac
    done < <(find "${root}" -type f -print0)
  done
  rm -f "${plain_matches}"

  if [ "${failures}" -ne 0 ]; then
    exit 1
  fi
}

binary_version="${PODLAZ_VERSION:-0.0.0~dev}"
package_version="${PODLAZ_DEB_VERSION:-${binary_version}-1}"
arch="${PODLAZ_DEB_ARCH:-amd64}"
out_dir="${PODLAZ_DIST_DIR:-dist}"
root_dir="${out_dir}/package-root"
config=".nfpm.podlaz.yaml"
version_package="github.com/AidarKhusainov/podlaz/internal/app/cli.version"
commit_package="github.com/AidarKhusainov/podlaz/internal/app/cli.commit"
built_package="github.com/AidarKhusainov/podlaz/internal/app/cli.built"
package="${out_dir}/podlaz_${package_version}_linux_${arch}.deb"

case "${arch}" in
  amd64|arm64) ;;
  *)
    echo "unsupported Debian architecture: ${arch}" >&2
    echo "supported values: amd64, arm64" >&2
    exit 2
    ;;
esac

goarch="$(deb_arch_to_goarch "${arch}")"

if [[ "${root_dir}" == *"#"* ]]; then
  echo "package root path contains an unsupported #: ${root_dir}" >&2
  exit 2
fi

if ! command -v go >/dev/null 2>&1; then
  echo "go is required to build podlaz binaries" >&2
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
  "${root_dir}/usr/lib/podlaz" \
  "${root_dir}/usr/lib/systemd/system" \
  "${root_dir}/usr/lib/sysusers.d" \
  "${root_dir}/usr/share/bash-completion/completions" \
  "${root_dir}/usr/share/zsh/vendor-completions" \
  "${root_dir}/usr/share/fish/vendor_completions.d" \
  "${root_dir}/usr/share/polkit-1/actions" \
  "${root_dir}/usr/share/man/man1" \
  "${root_dir}/usr/share/man/man8" \
  "${root_dir}/usr/share/doc/podlaz/third-party" \
  "${root_dir}/usr/share/lintian/overrides"

commit="${PODLAZ_COMMIT:-unknown}"
built="${PODLAZ_BUILT:-unknown}"
version_assignment="$(quote_go_ldflag_assignment "${version_package}=${binary_version}")"
commit_assignment="$(quote_go_ldflag_assignment "${commit_package}=${commit}")"
built_assignment="$(quote_go_ldflag_assignment "${built_package}=${built}")"
ldflags="-s -w -X ${version_assignment} -X ${commit_assignment} -X ${built_assignment}"
CGO_ENABLED=1 GOOS=linux GOARCH="${goarch}" go build -trimpath -ldflags "${ldflags}" -o "${root_dir}/usr/bin/podlaz" ./cmd/podlaz
CGO_ENABLED=1 GOOS=linux GOARCH="${goarch}" go build -trimpath -ldflags "${ldflags}" -o "${root_dir}/usr/bin/podlazd" ./cmd/podlazd
ln -s podlaz "${root_dir}/usr/bin/plz"

bash scripts/package-runtime-helpers.sh "${root_dir}" "${arch}" "${goarch}"

cat > "${root_dir}/usr/share/lintian/overrides/podlaz" <<'EOF'
podlaz: statically-linked-binary usr/lib/podlaz/xray
podlaz: unstripped-binary-or-object usr/lib/podlaz/xray
EOF
chmod 0644 "${root_dir}/usr/share/lintian/overrides/podlaz"

generate_completion bash "${root_dir}/usr/share/bash-completion/completions/podlaz"
generate_completion zsh "${root_dir}/usr/share/zsh/vendor-completions/_podlaz"
generate_completion fish "${root_dir}/usr/share/fish/vendor_completions.d/podlaz.fish"
chmod 0644 \
  "${root_dir}/usr/share/bash-completion/completions/podlaz" \
  "${root_dir}/usr/share/zsh/vendor-completions/_podlaz" \
  "${root_dir}/usr/share/fish/vendor_completions.d/podlaz.fish"
ln -s podlaz "${root_dir}/usr/share/bash-completion/completions/plz"
ln -s _podlaz "${root_dir}/usr/share/zsh/vendor-completions/_plz"
ln -s podlaz.fish "${root_dir}/usr/share/fish/vendor_completions.d/plz.fish"

install -m 0644 packaging/systemd/podlazd.service "${root_dir}/usr/lib/systemd/system/podlazd.service"
install -m 0644 packaging/sysusers.d/podlaz.conf "${root_dir}/usr/lib/sysusers.d/podlaz.conf"
install -m 0644 packaging/polkit-1/actions/io.github.aidarkhusainov.podlaz.policy "${root_dir}/usr/share/polkit-1/actions/io.github.aidarkhusainov.podlaz.policy"
gzip -9n -c docs/man/podlaz.1 > "${root_dir}/usr/share/man/man1/podlaz.1.gz"
gzip -9n -c docs/man/podlazd.8 > "${root_dir}/usr/share/man/man8/podlazd.8.gz"
install -m 0644 README.md LICENSE "${root_dir}/usr/share/doc/podlaz/"
install -m 0644 LICENSE "${root_dir}/usr/share/doc/podlaz/copyright"
printf 'podlaz (%s) unstable; urgency=medium\n\n  * Local development package build.\n\n -- Aidar Khusainov <19706697+AidarKhusainov@users.noreply.github.com>  Thu, 11 Jun 2026 00:00:00 +0000\n' "${package_version}" | gzip -9n -c > "${root_dir}/usr/share/doc/podlaz/changelog.Debian.gz"
find docs -type f ! -path 'docs/man/*' -print | while IFS= read -r file; do
  target="${root_dir}/usr/share/doc/podlaz/${file}"
  mkdir -p "$(dirname "${target}")"
  install -m 0644 "${file}" "${target}"
done
assert_no_obsolete_tun_artifacts "${root_dir}" "package root"

sed \
  -e "s#__PACKAGE_ROOT__#${root_dir}#g" \
  -e "s/__VERSION__/${package_version}/g" \
  -e "s/__ARCH__/${arch}/g" \
  packaging/nfpm.yaml > "${config}"

nfpm package --config "${config}" --packager deb --target "${out_dir}"
rm -f "${config}"

mapfile -t packages < <(find "${out_dir}" -maxdepth 1 -type f -name "podlaz_*_${arch}.deb" -print | sort)
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
