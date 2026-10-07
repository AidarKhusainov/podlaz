#!/usr/bin/env bash

declare -Ag REMNAWAVE_EVIDENCE=()
REMNAWAVE_FAILURE_CLASS=diagnostic_unknown
REMNAWAVE_FAILURE_STEP=bootstrap
REMNAWAVE_REPORT_FINALIZED=false

remnawave_mark_failure() {
  local class="$1" step="$2"
  case "${class}" in
    product|remnawave|fixture|infrastructure|capability|diagnostic_unknown|none) ;;
    *) class=diagnostic_unknown ;;
  esac
  REMNAWAVE_FAILURE_CLASS="${class}"
  REMNAWAVE_FAILURE_STEP="${step//[^A-Za-z0-9_.-]/_}"
}

remnawave_record() {
  REMNAWAVE_EVIDENCE["$1"]="$2"
}

remnawave_finalize_report() {
  local report="$1" commit="$2" digest="$3" panel_version="$4" node_version="$5"
  shift 5
  local key tmp="${report}.tmp"
  [[ "${REMNAWAVE_REPORT_FINALIZED}" == false ]] || return 0
  {
    printf 'candidate.commit=%s\n' "${commit,,}"
    printf 'candidate.package_sha256=%s\n' "${digest}"
    printf 'remnawave.panel_version=%s\n' "${panel_version}"
    printf 'remnawave.node_version=%s\n' "${node_version}"
    for key in "$@"; do
      printf '%s=%s\n' "${key}" "${REMNAWAVE_EVIDENCE[${key}]:-fail}"
    done
    printf 'failure.class=%s\n' "${REMNAWAVE_FAILURE_CLASS}"
    printf 'failure.step=%s\n' "${REMNAWAVE_FAILURE_STEP}"
  } >"${tmp}"
  chmod 0600 "${tmp}"
  mv -f -- "${tmp}" "${report}"
  REMNAWAVE_REPORT_FINALIZED=true
}

remnawave_mark_private_command_failure() {
  local private_root="$1" default_class="$2" default_step="$3"
  local marker="${private_root}/private-command/failed-command"
  local stderr_name="" stderr_file="" classification="" class="${default_class}" step="${default_step}"

  if [[ -f "${marker}" && ! -L "${marker}" ]]; then
    stderr_name="$(tr -d '\r\n' <"${marker}")"
    if [[ "${stderr_name}" =~ ^[0-9]{3}-([A-Za-z0-9._-]+)[.]stderr$ ]]; then
      step="${BASH_REMATCH[1]}"
      stderr_file="${private_root}/private-command/${stderr_name}"
      if [[ -f "${stderr_file}" && ! -L "${stderr_file}" ]]; then
        classification="$(
          python3 "${SCRIPT_DIR}/lib/tun_soak_metrics.py" classify-cli-error --stderr-file "${stderr_file}" 2>/dev/null
        )" || classification=unclassified
        case "${classification}" in
          authorization-denied|authorization-unavailable) class=capability ;;
          daemon-unavailable) class=infrastructure ;;
          daemon-internal) class=product ;;
          unclassified|"") class="${default_class}" ;;
          *) class=diagnostic_unknown ;;
        esac
      fi
    fi
  fi

  remnawave_mark_failure "${class}" "${step}"
}
