# shellcheck shell=bash

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
