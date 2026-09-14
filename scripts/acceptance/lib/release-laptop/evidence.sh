RA_RESUME_DIAGNOSTIC="${RA_RESUME_DIAGNOSTIC:-/run/podlaz/diagnostics/network-session-resume.json}"

ra_public_resource_summary() { local v="$1"; jq -c '{sample_count,measured_seconds,daemon,xray,service,identity:{daemon_generation_count:.identity.daemon_generation_count,xray_generation_count:.identity.xray_generation_count}}' <<<"$v"; }

ra_public_scenarios() {
  jq -c '
    def public_outcome:
      if ((.outcome // "") != "") then .outcome
      elif (.state // "") == "failed" then "FAIL"
      elif (.state // "") == "passed" then "PASS"
      elif ((.state // "") == "prepared" or (.state // "") == "running" or (.state // "") == "verifying") then "IN_PROGRESS"
      else "NOT_EXERCISED"
      end;
    .scenarios
    | with_entries(
        .value as $value
        | ($value | public_outcome) as $outcome
        | .value={
            outcome:$outcome,
            state:($value.state // ""),
            skip_class:(
              if ($outcome | startswith("SKIP_HOST_CAPABILITY")) then "HOST_CAPABILITY"
              elif ($outcome | startswith("SKIP_REMOTE")) then "REMOTE_SESSION"
              elif ($outcome | startswith("SKIP_USER")) then "USER_REQUEST"
              else null
              end
            )
          }
      )
  ' "$RA_CHECKPOINT"
}

ra_report_write() {
  local qualification="$1" summary="$RA_PUBLIC_DIR/summary.txt" report="$RA_PUBLIC_DIR/report.json" req="$RA_PUBLIC_DIR/requirements-observation.json"
  local first second compare scenarios first_public=null second_public=null actual=0
  mkdir -p "$RA_PUBLIC_DIR" || return 1
  first="$(jq -c '.private.resource.first_session_summary//null' "$RA_CHECKPOINT")"
  second="$(jq -c '.private.resource.second_session_summary//null' "$RA_CHECKPOINT")"
  compare="$(jq -c '.private.resource.session_comparison//null' "$RA_CHECKPOINT")"
  actual="$(jq -r '.private.resource.actual_soak_seconds//0' "$RA_CHECKPOINT")"
  scenarios="$(ra_public_scenarios)" || return 1
  [[ "$first" == null ]] || first_public="$(ra_public_resource_summary "$first")"
  [[ "$second" == null ]] || second_public="$(ra_public_resource_summary "$second")"
  {
    printf 'Podlaz release laptop acceptance\nResult: %s\nActual soak duration: %s seconds\n' "$qualification" "$actual"
    jq -r 'to_entries|sort_by(.key)[]|"\(.key): \(.value.outcome)"' <<<"$scenarios"
    if [[ "$first_public" != null ]]; then
      printf '\nResource observations:\n'
      jq -r '"  samples: \(.sample_count)\n  daemon RSS median/peak/last KiB: \(.daemon.rss_kb.median)/\(.daemon.rss_kb.sampled_peak)/\(.daemon.rss_kb.last)\n  Xray RSS median/peak/last KiB: \(.xray.rss_kb.median)/\(.xray.rss_kb.sampled_peak)/\(.xray.rss_kb.last)\n  cgroup memory.current median/peak/last: \(.service.memory_current.median)/\(.service.memory_current.sampled_peak)/\(.service.memory_current.last)\n  service lifetime memory.peak: \(.service.lifetime_memory_peak)"' <<<"$first_public"
    fi
  } >"$summary" || return 1
  jq \
    --arg q "$qualification" \
    --argjson actual "$actual" \
    --argjson first "$first_public" \
    --argjson second "$second_public" \
    --argjson comparison "$compare" \
    --argjson scenarios "$scenarios" \
    '{schema_version:"podlaz.release-acceptance-report.v4",qualification:$q,configured_soak_minutes:.private.run_config.soak_minutes,actual_soak_seconds:$actual,scenarios:$scenarios,privacy_evidence:{candidate_active:($scenarios.privacy_active.outcome//"NOT_EXERCISED"),classification:"functional_direct_tripwire_plus_exact_local_authority"},mutation_states:[.mutations|to_entries[]|{kind:.value.kind,state:.value.state}],resources:{first_session:$first,second_session:$second,comparison:(if $comparison==null then null else ($comparison|del(.first_session_id,.second_session_id,.warmed_inactive_baseline)) end)}}' \
    "$RA_CHECKPOINT" >"$report" || return 1
  jq -n --arg arch "$(uname -m 2>/dev/null || true)" --arg kernel "$(uname -r 2>/dev/null || true)" --argjson soak "$RA_SOAK_MINUTES" --argjson actual "$actual" --argjson first "$first_public" '{schema_version:"podlaz.release-requirements-observation.v1",classification:"single-host-observation",architecture:$arch,kernel:$kernel,configured_soak_minutes:$soak,actual_soak_seconds:$actual,resource_observation:$first}' >"$req" || return 1
  chmod 0600 "$summary" "$report" "$req" || return 1
  if ((EUID==0)) && [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" != 1 ]]; then chown "$RA_UID:$RA_GID" "$summary" "$report" "$req" || return 1; fi
}

ra_qualification() { jq -e '.scenarios[]?|select(.outcome=="FAIL")' "$RA_CHECKPOINT" >/dev/null && { printf FAIL; return 0; }; local required=(lower_release_upgrade privacy_active graceful_restart daemon_kill reinstall rollback_interruption stop_start_no_reconnect preconnect_coexistence active_coexistence resource_soak disconnect_cleanup coexistence_reconnect reconnect_resource_nonaccumulation runtime_terminal_convergence runtime_terminal_no_retry final_restoration) r; if ((RA_REBOOT_PHASES==1)); then required+=(reboot_autostart_off reboot_autostart_on explicit_disconnect_no_same_boot_retry reboot_terminal_autostart terminal_no_same_boot_retry); fi; for r in "${required[@]}"; do [[ "$(jq -r --arg r "$r" '.scenarios[$r].outcome//""' "$RA_CHECKPOINT")" == PASS ]] || { printf FAIL; return 0; }; done; if [[ "$RA_SOAK_MINUTES" != 60 ]] || ((RA_REBOOT_PHASES==0 || RA_ALLOW_WIFI==0 || RA_ALLOW_SUSPEND==0)); then printf PARTIAL_PASS; return 0; fi; local w s; w="$(jq -r '.scenarios.wifi_reconnect.outcome//""' "$RA_CHECKPOINT")"; s="$(jq -r '.scenarios.suspend_resume.outcome//""' "$RA_CHECKPOINT")"; [[ "$w" == PASS || "$w" == SKIP_HOST_CAPABILITY || "$w" == SKIP_REMOTE_SESSION ]] || { printf PARTIAL_PASS; return 0; }; [[ "$s" == PASS || "$s" == SKIP_HOST_CAPABILITY || "$s" == SKIP_REMOTE_SESSION ]] || { printf PARTIAL_PASS; return 0; }; printf QUALIFIED_PASS; }
ra_final_restoration_verify() { local candidate installed spec; candidate="$(jq -ce '.candidate' "$RA_CHECKPOINT")" || return 1; installed="$(ra_pkg_installed_version)" || return 1; [[ "$installed" == "$(jq -r '.version' <<<"$candidate")" ]] || return 1; ra_verify_inactive_boundary || return 1; ra_privacy_require_ordinary || return 1; ra_require_mutations_released || return 1; [[ ! -e "$RA_ROLLBACK_OVERRIDE" && ! -e "$RA_TERMINAL_OVERRIDE" && ! -e "$RA_ROLLBACK_HOOK_DIR" && ! -e "$RA_TERMINAL_HOOK_DIR" ]] || return 1; spec="$(ra_fixture_spec fixture_a)" || return 1; ra_fixture_assert_free "$spec" || return 1; spec="$(ra_fixture_spec fixture_b)" || return 1; ra_fixture_assert_free "$spec" || return 1; ra_terminal_profile_reconcile || return 1; ra_restore_service_state || return 1; ra_verify_run_tree; }
ra_finalize() { ra_restore_original_policy || return 1; ra_final_restoration_verify || { ra_record final_restoration FAIL "restoration verification failed" || true; return 1; }; ra_record final_restoration PASS || return 1; local q; q="$(ra_qualification)" || return 1; ra_report_write "$q" || return 1; ra_verify_run_tree || return 1; ra_set_phase complete || return 1; if [[ "$q" != FAIL ]]; then ra_state_remove || return 1; fi; printf '%s\n' "$q"; [[ "$q" != FAIL ]]; }

ra_failure_class() { case "$1" in host_capability*|*HOST_CAPABILITY*) printf HOST_CAPABILITY ;; host_state*|*HOST_STATE*) printf HOST_STATE ;; signal_*|interrupted*) printf INTERRUPTED ;; *ownership*|*ambiguous*|*cleanup*) printf OWNERSHIP ;; *schema*|*invariant*|*internal*|*contract*) printf INTERNAL ;; preflight*|input*) printf INPUT ;; *) printf PRODUCT ;; esac; }
ra_failure_policy() { case "$1" in HOST_CAPABILITY|HOST_STATE) printf 'RETRY\tfix host capability/state and rerun' ;; INPUT) printf 'RETRY\tfix input and rerun' ;; OWNERSHIP) printf 'MANUAL_DIAGNOSIS\tinspect retained authority before mutation' ;; INTERRUPTED) printf 'RETRY\trerun the same command at the persisted boundary' ;; INTERNAL) printf 'RESTART\tfix harness/contract incompatibility, then restart qualification' ;; PRODUCT) printf 'RESTART\trestart qualification after reviewing product evidence' ;; *) printf 'MANUAL_DIAGNOSIS\tinspect evidence' ;; esac; }
ra_failure_record() { local reason="$1" exit_code="$2" class policy retry action occurred scenario boot progress; class="$(ra_failure_class "$reason")"; policy="$(ra_failure_policy "$class")"; IFS=$'\t' read -r retry action <<<"$policy"; occurred="$(date -u +%Y-%m-%dT%H:%M:%SZ)"; scenario="$(jq -r '.current_scenario//""' "$RA_CHECKPOINT" 2>/dev/null || true)"; boot="$(ra_boot_id 2>/dev/null || true)"; progress="$(jq -c --arg s "$scenario" '{phase:.phase,scenario_state:(if $s=="" then null else (.scenarios[$s].state//null) end)}' "$RA_CHECKPOINT" 2>/dev/null || printf '{}')"; ra_state_jq '.current_boot_id=$boot|.last_failure={step:$step,scenario:$scenario,class:$class,exit_code:$exit,retry_policy:$retry,recommended_action:$action,occurred_at:$at,boot_id:$boot,progress:$progress,cleanup_outcome:"pending"}' --arg step "$reason" --arg scenario "$scenario" --arg class "$class" --argjson exit "$exit_code" --arg retry "$retry" --arg action "$action" --arg at "$occurred" --arg boot "$boot" --argjson progress "$progress"; }

ra_failure_component_set() {
  local components="$1" name="$2" observation="$3" applicability="${4:-required}" absence_satisfies="${5:-0}"
  local status="" complete=false
  case "$observation" in captured|verified_absent|command_failed|unavailable|timeout|invalid) ;; *) return 1 ;; esac
  case "$applicability" in required|optional|not_applicable) ;; *) return 1 ;; esac
  case "$absence_satisfies" in 0|1) ;; *) return 1 ;; esac

  if [[ "$applicability" != required || "$observation" == captured || ( "$observation" == verified_absent && "$absence_satisfies" == 1 ) ]]; then
    complete=true
  fi
  case "$observation" in
    captured|command_failed|unavailable) status="$observation" ;;
    verified_absent) status=unavailable ;;
  esac

  if [[ -n "$status" ]]; then
    jq -c \
      --arg name "$name" --arg status "$status" --arg observation "$observation" --arg applicability "$applicability" \
      --argjson complete "$complete" --argjson absence_satisfies "$([[ "$absence_satisfies" == 1 ]] && printf true || printf false)" \
      '.[$name]={status:$status,observation:$observation,applicability:$applicability,complete:$complete,absence_satisfies_requirement:$absence_satisfies}' <<<"$components"
  else
    jq -c \
      --arg name "$name" --arg observation "$observation" --arg applicability "$applicability" \
      --argjson complete "$complete" --argjson absence_satisfies "$([[ "$absence_satisfies" == 1 ]] && printf true || printf false)" \
      '.[$name]={observation:$observation,applicability:$applicability,complete:$complete,absence_satisfies_requirement:$absence_satisfies}' <<<"$components"
  fi
}

ra_failure_capture_command() {
  local file="$1"
  shift
  local rc status
  if ra_capture "$@"; then rc=0; status=captured; else rc="$RA_CAPTURE_RC"; status=command_failed; fi
  { printf 'rc=%s\n' "$rc"; printf '%s\n' "$RA_CAPTURE"; } | ra_artifact_file_write "$file" || return 1
  printf '%s' "$status"
}

ra_failure_capture_doctor_tun() {
  local file="$1" rc observation diagnostic_status=""
  RA_FAILURE_CAPTURE_OBSERVATION=""
  RA_FAILURE_DOCTOR_STATUS=""
  RA_FAILURE_DOCTOR_RC=0

  if ra_product doctor --tun --json >/dev/null; then
    rc="$RA_CAPTURE_RC"
  else
    rc="$RA_CAPTURE_RC"
  fi
  [[ "$rc" =~ ^[0-9]+$ ]] || rc=1
  { printf 'rc=%s\n' "$rc"; printf '%s\n' "$RA_CAPTURE"; } | ra_artifact_file_write "$file" || return 1

  if [[ "$rc" == 124 ]]; then
    observation=timeout
  elif [[ "$rc" == 0 || "$rc" == 3 ]]; then
    if diagnostic_status="$(jq -er 'select(.schema_version==1) | .status | select(.=="healthy" or .=="degraded" or .=="unhealthy" or .=="unavailable")' <<<"$RA_CAPTURE" 2>/dev/null)"; then
      if { [[ "$rc" == 0 ]] && [[ "$diagnostic_status" == healthy || "$diagnostic_status" == degraded ]]; } || \
         { [[ "$rc" == 3 ]] && [[ "$diagnostic_status" == unhealthy || "$diagnostic_status" == unavailable ]]; }; then
        observation=captured
      else
        observation=invalid
      fi
    else
      observation=invalid
      diagnostic_status=""
    fi
  else
    observation=command_failed
  fi

  RA_FAILURE_CAPTURE_OBSERVATION="$observation"
  RA_FAILURE_DOCTOR_STATUS="$diagnostic_status"
  RA_FAILURE_DOCTOR_RC="$rc"
  printf '%s' "$observation"
}

ra_failure_copy_private_file() {
  local source="$1" target="$2" size
  if [[ -L "$source" ]]; then printf invalid; return 0; fi
  if [[ ! -e "$source" ]]; then printf verified_absent; return 0; fi
  [[ -f "$source" ]] || { printf invalid; return 0; }
  size="$(stat -Lc '%s' "$source" 2>/dev/null)" || { printf command_failed; return 0; }
  [[ "$size" =~ ^[0-9]+$ && "$size" -le 1048576 ]] || { printf invalid; return 0; }
  if cat "$source" | ra_artifact_file_write "$target"; then printf captured; else return 1; fi
}

ra_failure_capture_boot_attempt() {
  local source="$1" target="$2" expected_boot="$3" size mode
  if [[ -L "$source" ]]; then printf invalid; return 0; fi
  if [[ ! -e "$source" ]]; then printf verified_absent; return 0; fi
  [[ -f "$source" ]] || { printf invalid; return 0; }
  size="$(stat -Lc '%s' "$source" 2>/dev/null)" || { printf command_failed; return 0; }
  mode="$(stat -Lc '%a' "$source" 2>/dev/null)" || { printf command_failed; return 0; }
  [[ "$size" =~ ^[0-9]+$ && "$size" -le 65536 && "$mode" == 600 ]] || { printf invalid; return 0; }
  jq -e --arg boot "$expected_boot" '
    .schema_version=="podlaz.boot-autostart-attempt.v1" and
    .boot_id==$boot and
    (.manifest_generation|type)=="string" and
    (.manifest_generation|test("^[0-9a-f]{32}$")) and
    (.configuration|type)=="object" and
    (.state=="in_progress" or .state=="succeeded" or .state=="terminal") and
    (if .state=="terminal" then (.terminal_reason=="connect_failed" or .terminal_reason=="session_terminal" or .terminal_reason=="network_not_ready") else ((.terminal_reason//"")=="") end)
  ' "$source" >/dev/null 2>&1 || { printf invalid; return 0; }
  if cat "$source" | ra_artifact_file_write "$target"; then printf captured; else return 1; fi
}

ra_failure_capture_replay_diagnostic() {
  local target="$1" source="$RA_RESUME_DIAGNOSTIC" size mode
  if [[ -L "$source" ]]; then printf invalid; return 0; fi
  if [[ ! -e "$source" ]]; then printf verified_absent; return 0; fi
  [[ -f "$source" ]] || { printf invalid; return 0; }
  size="$(stat -Lc '%s' "$source" 2>/dev/null)" || { printf command_failed; return 0; }
  mode="$(stat -Lc '%a' "$source" 2>/dev/null)" || { printf command_failed; return 0; }
  [[ "$size" =~ ^[0-9]+$ && "$size" -le 16384 && "$mode" == 600 ]] || { printf invalid; return 0; }
  jq -e '
    .schema_version=="podlaz.network-session-resume-diagnostic.v1" and
    .owner=="podlaz" and
    (.boot_id|type)=="string" and (.boot_id|length)>0 and
    (.recovery_epoch|type)=="number" and
    (.resume_stage|type)=="string" and (.resume_stage|length)>0 and
    (.last_resume_outcome|type)=="string" and (.last_resume_outcome|length)>0 and
    (.transaction_present|type)=="boolean" and
    ((has("legacy_migration")|not) or (.legacy_migration|type)=="boolean") and
    ((.replay_disposition//"") as $v | $v=="" or $v=="terminal" or $v=="retryable" or $v=="interrupted" or $v=="incomplete") and
    ((has("network_apply_subphase")|not) or (.network_apply_subphase|type)=="string")
  ' "$source" >/dev/null 2>&1 || { printf invalid; return 0; }
  if cat "$source" | ra_artifact_file_write "$target"; then printf captured; else return 1; fi
}

ra_failure_attempts_validate() {
  local failures="$1" path name metadata
  ra_artifact_dir_validate "$failures" || return 1
  while IFS= read -r -d '' path; do
    name="$(basename -- "$path")"
    [[ "$name" =~ ^20[0-9]{6}T[0-9]{6}\.[0-9]{9}Z-[0-9]{3}$ ]] || return 1
    ra_artifact_dir_validate "$path" || return 1
    metadata="$path/metadata.json"
    ra_artifact_file_validate "$metadata" || return 1
    jq -e --arg id "$name" '.attempt_id==$id and (.root_failure_id|type)=="string"' "$metadata" >/dev/null || return 1
  done < <(find "$failures" -mindepth 1 -maxdepth 1 -print0 2>/dev/null)
}

ra_failure_next_attempt_id() {
  local failures="$1" stamp id
  local -i seq
  stamp="$(date -u +%Y%m%dT%H%M%S.%NZ)" || return 1
  for ((seq=0; seq<=999; seq++)); do
    printf -v id '%s-%03d' "$stamp" "$seq"
    [[ ! -e "$failures/$id" && ! -L "$failures/$id" ]] || continue
    printf '%s' "$id"
    return 0
  done
  return 1
}

ra_failure_process_identity() {
  local pid statline start exe cgroup
  pid="$(ra_main_pid 2>/dev/null || true)"
  [[ "$pid" =~ ^[0-9]+$ && "$pid" -gt 1 && -r "/proc/$pid/stat" ]] || return 1
  statline="$(cat "/proc/$pid/stat")" || return 1
  start="$(awk '{print $22}' <<<"$statline")" || return 1
  exe="$(readlink -f "/proc/$pid/exe" 2>/dev/null || true)"
  cgroup="$(awk -F: '$1==0{print $3;exit}' "/proc/$pid/cgroup" 2>/dev/null || true)"
  jq -cn --argjson pid "$pid" --arg start "$start" --arg exe "$exe" --arg cgroup "$cgroup" '{pid:$pid,start_time_ticks:$start,exe:$exe,cgroup_path:$cgroup}'
}

ra_failure_dpkg_slice() {
  local source="$1" since="$2" target="$3" boundary
  [[ -f "$source" && ! -L "$source" ]] || { printf unavailable; return 0; }
  boundary="$(date -u -d "$since" '+%Y-%m-%d %H:%M:%S' 2>/dev/null)" || { printf unavailable; return 0; }
  if awk -v boundary="$boundary" '$0 ~ /podlaz/ {ts=$1" "$2; if (ts>=boundary) print substr($0,1,4096)}' "$source" | tail -n 2000 | ra_artifact_file_write "$target"; then
    printf captured
  else
    return 1
  fi
}

ra_failure_boot_attempt_policy() {
  case "$1" in
    reboot_autostart_off) printf 'required\t1' ;;
    reboot_autostart_on|explicit_disconnect_no_same_boot_retry|reboot_terminal_autostart|terminal_no_same_boot_retry) printf 'required\t0' ;;
    *) printf 'not_applicable\t1' ;;
  esac
}

ra_failure_bundle_capture() {
  local reason="${1:-failure}" exit_code="${2:-1}" invocation_mode="${3:-$RA_MODE}"
  local failures attempt_id final tmp previous_path previous_id="" root_id="" created_at scenario scenario_state phase started boot boot_canonical
  local components='{}' status value package_identity process_identity capture_status dpkg_log_source
  local boot_attempt_applicability boot_attempt_absence replay_applicability replay_absence replay_required=false
  [[ -n "$RA_PRIVATE_DIR" ]] || return 1
  ra_artifact_dir_validate "$RA_PRIVATE_DIR" || return 1
  failures="$RA_PRIVATE_DIR/failures"
  ra_artifact_dir_ensure "$failures" || return 1
  ra_failure_attempts_validate "$failures" || return 1

  previous_path="$(find "$failures" -mindepth 1 -maxdepth 1 -type d -name '20*' -print | sort | tail -n1)"
  if [[ -n "$previous_path" ]]; then
    previous_id="$(basename -- "$previous_path")"
    root_id="$(jq -er '.root_failure_id' "$previous_path/metadata.json")" || return 1
  fi
  attempt_id="$(ra_failure_next_attempt_id "$failures")" || return 1
  [[ -n "$root_id" ]] || root_id="$attempt_id"
  final="$failures/$attempt_id"
  tmp="$failures/.capture-$attempt_id-$$"
  [[ ! -e "$tmp" && ! -L "$tmp" ]] || return 1
  ra_artifact_dir_ensure "$tmp" || return 1

  created_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)" || { ra_artifact_as_user rm -rf -- "$tmp"; return 1; }
  scenario="$(jq -r '.current_scenario//""' "$RA_CHECKPOINT" 2>/dev/null || true)"
  phase="$(jq -r '.phase//""' "$RA_CHECKPOINT" 2>/dev/null || true)"
  scenario_state="$(jq -r --arg s "$scenario" 'if $s=="" then "" else (.scenarios[$s].state//"") end' "$RA_CHECKPOINT" 2>/dev/null || true)"
  if [[ -n "$scenario" ]]; then
    started="$(jq -r --arg s "$scenario" '.scenarios[$s].started_at//.run_started_at//""' "$RA_CHECKPOINT")"
  else
    started="$(jq -r '.run_started_at//""' "$RA_CHECKPOINT")"
  fi
  boot="$(jq -r '.last_failure.boot_id//.current_boot_id//.starting_boot_id//""' "$RA_CHECKPOINT")"
  boot_canonical="$(ra_boot_id_normalize "$boot" 2>/dev/null || true)"
  replay_required="$(jq -r --arg scenario "$scenario" '($scenario=="lower_release_upgrade") and (.mutations.candidate_upgrade.identity.applied//false)' "$RA_CHECKPOINT" 2>/dev/null || printf false)"

  if cat "$RA_CHECKPOINT" | ra_artifact_file_write "$tmp/checkpoint.json"; then status=captured; else status=command_failed; fi
  components="$(ra_failure_component_set "$components" checkpoint "$status")" || return 1

  if [[ -f "$RA_TRANSCRIPT" && ! -L "$RA_TRANSCRIPT" ]]; then
    if cat "$RA_TRANSCRIPT" | ra_artifact_file_write "$tmp/commands.log" && tail -n 200 "$RA_TRANSCRIPT" | ra_artifact_file_write "$tmp/last-commands.log"; then status=captured; else status=command_failed; fi
  else status=unavailable; fi
  components="$(ra_failure_component_set "$components" commands "$status")" || return 1

  if value="$(ra_status_json 2>/dev/null)"; then
    printf '%s\n' "$value" | ra_artifact_file_write "$tmp/status.txt" || return 1
    status=captured
  else
    printf 'status capture failed rc=%s\n' "$?" | ra_artifact_file_write "$tmp/status.txt" || return 1
    status=command_failed
  fi
  components="$(ra_failure_component_set "$components" status "$status")" || return 1

  ra_failure_capture_doctor_tun "$tmp/doctor-tun.txt" >/dev/null || return 1
  status="$RA_FAILURE_CAPTURE_OBSERVATION"
  components="$(ra_failure_component_set "$components" doctor "$status")" || return 1
  components="$(jq -c --arg diagnostic_status "$RA_FAILURE_DOCTOR_STATUS" --argjson exit_code "$RA_FAILURE_DOCTOR_RC" '.doctor += {diagnostic_status:(if $diagnostic_status=="" then null else $diagnostic_status end),exit_code:$exit_code}' <<<"$components")" || return 1

  status="$(ra_failure_capture_command "$tmp/systemd-unit-properties.txt" systemctl show "$RA_SERVICE" -p MainPID -p ActiveState -p SubState -p Result -p ExecMainCode -p ExecMainStatus -p KillSignal -p RestartKillSignal -p KillMode -p TimeoutStopUSec -p FragmentPath)" || return 1
  components="$(ra_failure_component_set "$components" systemd_unit_properties "$status")" || return 1
  status="$(ra_failure_capture_command "$tmp/systemd-version.txt" systemd --version)" || return 1
  components="$(ra_failure_component_set "$components" systemd_version "$status")" || return 1
  status="$(ra_failure_capture_command "$tmp/systemctl-status.txt" systemctl status "$RA_SERVICE" --no-pager --full)" || return 1
  components="$(ra_failure_component_set "$components" systemctl_status "$status")" || return 1
  status="$(ra_failure_capture_command "$tmp/host-kernel.txt" uname -a)" || return 1
  components="$(ra_failure_component_set "$components" host_identity "$status")" || return 1

  if [[ -n "$boot_canonical" && -n "$started" ]]; then
    status="$(ra_failure_capture_command "$tmp/journal.txt" journalctl -u "$RA_SERVICE" "_BOOT_ID=$boot_canonical" --since "$started" --no-pager -o short-iso -n 2000)" || return 1
  else
    printf 'rc=unavailable\ninvalid boot identity or time boundary\n' | ra_artifact_file_write "$tmp/journal.txt" || return 1
    status=unavailable
  fi
  components="$(ra_failure_component_set "$components" journal "$status")" || return 1

  status="$(ra_failure_capture_command "$tmp/package-state.txt" dpkg-query -W '-f=${Status}\t${Version}\t${Architecture}\n' podlaz)" || return 1
  components="$(ra_failure_component_set "$components" package_state "$status")" || return 1

  if package_identity="$(jq -ce '.candidate|{package,version,architecture,sha256}' "$RA_CHECKPOINT" 2>/dev/null)"; then
    printf '%s\n' "$package_identity" | ra_artifact_file_write "$tmp/package-identity.json" || return 1
    status=captured
  else status=unavailable; fi
  components="$(ra_failure_component_set "$components" package_identity "$status")" || return 1

  dpkg_log_source="${RELEASE_ACCEPTANCE_TEST_DPKG_LOG:-/var/log/dpkg.log}"
  status="$(ra_failure_dpkg_slice "$dpkg_log_source" "$started" "$tmp/dpkg-log.txt")" || return 1
  components="$(ra_failure_component_set "$components" dpkg_log "$status")" || return 1

  status="$(ra_failure_copy_private_file "$RA_CONTINUATION" "$tmp/network-session.json")" || return 1
  components="$(ra_failure_component_set "$components" network_session "$status" required 0)" || return 1

  status="$(ra_failure_capture_boot_attempt "$RA_BOOT_ATTEMPT" "$tmp/boot-autostart-attempt.json" "$boot")" || return 1
  IFS=$'\t' read -r boot_attempt_applicability boot_attempt_absence <<<"$(ra_failure_boot_attempt_policy "$scenario")"
  components="$(ra_failure_component_set "$components" boot_attempt "$status" "$boot_attempt_applicability" "$boot_attempt_absence")" || return 1

  status="$(ra_failure_capture_replay_diagnostic "$tmp/network-session-resume.json")" || return 1
  if [[ "$status" == verified_absent && "$replay_required" != true ]]; then
    replay_applicability=optional
    replay_absence=1
  else
    replay_applicability=required
    replay_absence=0
  fi
  components="$(ra_failure_component_set "$components" replay_diagnostic "$status" "$replay_applicability" "$replay_absence")" || return 1

  if process_identity="$(ra_failure_process_identity 2>/dev/null)"; then
    printf '%s\n' "$process_identity" | ra_artifact_file_write "$tmp/daemon-process-identity.json" || return 1
    status=captured
  else status=unavailable; fi
  components="$(ra_failure_component_set "$components" daemon_process_identity "$status")" || return 1

  if jq '.mutations' "$RA_CHECKPOINT" | ra_artifact_file_write "$tmp/mutation-ledger.json"; then status=captured; else status=command_failed; fi
  components="$(ra_failure_component_set "$components" mutation_ledger "$status")" || return 1
  if jq '{phase,current_scenario,last_failure,scenarios}' "$RA_CHECKPOINT" | ra_artifact_file_write "$tmp/run-state.json"; then status=captured; else status=command_failed; fi
  components="$(ra_failure_component_set "$components" run_state "$status")" || return 1

  status="$(ra_failure_capture_command "$tmp/ip-links.json" ip -j -d link show)" || return 1
  components="$(ra_failure_component_set "$components" ip_links "$status")" || return 1
  status="$(ra_failure_capture_command "$tmp/ip-routes.json" ip -j -4 route show table all)" || return 1
  components="$(ra_failure_component_set "$components" ip_routes "$status")" || return 1
  status="$(ra_failure_capture_command "$tmp/ip-rules.txt" ip -4 rule show)" || return 1
  components="$(ra_failure_component_set "$components" ip_rules "$status")" || return 1
  status="$(ra_failure_capture_command "$tmp/nft-ruleset.json" nft -j list ruleset)" || return 1
  components="$(ra_failure_component_set "$components" nft_ruleset "$status")" || return 1
  status="$(ra_failure_capture_command "$tmp/resolved-status.txt" resolvectl status --no-pager)" || return 1
  components="$(ra_failure_component_set "$components" resolved_status "$status")" || return 1

  if jq -e 'to_entries|all(.value.complete==true)' <<<"$components" >/dev/null; then capture_status=complete; else capture_status=partial; fi
  jq -cn \
    --arg attempt "$attempt_id" --arg root "$root_id" --arg previous "$previous_id" \
    --arg created "$created_at" --arg mode "$invocation_mode" --arg phase "$phase" \
    --arg scenario "$scenario" --arg scenario_state "$scenario_state" --arg reason "$reason" \
    --arg class "$(ra_failure_class "$reason")" --argjson exit_code "$exit_code" \
    --arg boot "$boot_canonical" --arg since "$started" --arg capture_status "$capture_status" \
    --argjson components "$components" --argjson package "${package_identity:-null}" --argjson process "${process_identity:-null}" \
    '{attempt_id:$attempt,root_failure_id:$root,previous_bundle_id:(if $previous=="" then null else $previous end),created_at:$created,invocation_mode:$mode,phase:$phase,current_scenario:$scenario,scenario_state:$scenario_state,failure:{reason:$reason,class:$class,exit_code:$exit_code},boot_id:$boot,time_boundary:$since,package_identity:$package,daemon_process_identity:$process,capture_status:$capture_status,components:$components}' \
    | ra_artifact_file_write "$tmp/metadata.json" || { ra_artifact_as_user rm -rf -- "$tmp" || true; return 1; }

  ra_artifact_dir_validate "$tmp" || return 1
  [[ ! -e "$final" && ! -L "$final" ]] || return 1
  sync -f "$tmp" 2>/dev/null || true
  ra_artifact_as_user mv -- "$tmp" "$final" || return 1
  ra_artifact_dir_validate "$final" || return 1
  ra_artifact_file_validate "$final/metadata.json" || return 1
  sync -f "$failures" 2>/dev/null || true
}

ra_failure_reason_effective() { local reason="$1"; if [[ "$reason" != operation_failed ]]; then printf '%s' "$reason"; return; fi; if [[ -n "$RA_LAST_FAILURE_REASON" ]]; then printf '%s' "$RA_LAST_FAILURE_REASON"; return; fi; if [[ -s "$RA_PRIVATE_DIR/last-error-reason" ]]; then head -n1 "$RA_PRIVATE_DIR/last-error-reason"; return; fi; printf 'internal_operation_failed_without_classification'; }
ra_failure_finalize() { local reason exit_code="${2:-1}"; reason="$(ra_failure_reason_effective "${1:-unexpected_failure}")"; if ((RA_FINALIZER_ACTIVE==1)); then return 1; fi; ra_checkpoint_exists >/dev/null 2>&1 || return 1; if ra_phase_blocks_auto_finalizer; then return 1; fi; RA_FINALIZER_ACTIVE=1; ra_failure_record "$reason" "$exit_code" || { RA_FINALIZER_ACTIVE=0; return 1; }; ra_failure_bundle_capture "$reason" "$exit_code" automatic-finalizer || true; ra_state_jq '.phase="failure-cleanup-running"' || { RA_FINALIZER_ACTIVE=0; return 1; }; if ra_safe_cleanup; then ra_state_jq '.phase="failed-clean"|.last_failure.cleanup_outcome="clean"' || return 1; if ! ra_report_write FAILED_CLEAN; then ra_state_jq '.phase="fail-cleanup-failed"|.last_failure.class="OWNERSHIP"|.last_failure.cleanup_outcome="report_failed"|.last_failure.retry_policy="MANUAL_DIAGNOSIS"' || true; printf 'FAIL_CLEANUP_FAILED\n'; RA_FINALIZER_ACTIVE=0; return 1; fi; ra_verify_run_tree || { RA_FINALIZER_ACTIVE=0; return 1; }; ra_state_remove || return 1; printf 'FAILED_CLEAN\n'; RA_FINALIZER_ACTIVE=0; return 1; fi; ra_state_jq '.phase="fail-cleanup-failed"|.last_failure.class="OWNERSHIP"|.last_failure.cleanup_outcome="failed"|.last_failure.retry_policy="MANUAL_DIAGNOSIS"' || true; ra_report_write FAIL_CLEANUP_FAILED || true; printf 'FAIL_CLEANUP_FAILED\n'; RA_FINALIZER_ACTIVE=0; return 1; }
ra_signal_handler() { local signal="$1" exit_code=130; [[ "$signal" != TERM ]] || exit_code=143; if ra_checkpoint_exists >/dev/null 2>&1 && ! ra_phase_blocks_auto_finalizer; then ra_failure_finalize "signal_${signal}" "$exit_code" || true; fi; return 1; }
