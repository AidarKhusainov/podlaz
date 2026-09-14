# Boot attempt/session authority helpers.
ra_boot_attempt_snapshot() { local boot; boot="$(ra_boot_id)" || return 1; [[ -f "$RA_BOOT_ATTEMPT" && ! -L "$RA_BOOT_ATTEMPT" ]] || return 1; jq -ce --arg boot "$boot" 'select(.schema_version=="podlaz.boot-autostart-attempt.v1" and .boot_id==$boot and (.manifest_generation|type)=="string" and (.configuration|type)=="object" and (.state=="in_progress" or .state=="succeeded" or .state=="terminal"))' "$RA_BOOT_ATTEMPT"; }
ra_boot_attempt_assert_absent() { [[ ! -e "$RA_BOOT_ATTEMPT" && ! -L "$RA_BOOT_ATTEMPT" ]]; }
ra_same_boot_restart_stays_inactive() { local scenario="$1" before current requested attempt_before="" attempt_after=""; before="$(jq -r --arg n "$scenario" '.scenarios[$n].private.before_pid//""' "$RA_CHECKPOINT")"; requested="$(jq -r --arg n "$scenario" '.scenarios[$n].private.mutation_requested//false' "$RA_CHECKPOINT")"; current="$(ra_main_pid)" || return 1; if [[ -f "$RA_BOOT_ATTEMPT" ]]; then attempt_before="$(ra_boot_attempt_snapshot)" || return 1; fi; if [[ -n "$before" && "$current" != "$before" ]]; then ra_wait_inactive 30 || return 1; else if [[ -n "$before" && "$requested" == true ]]; then ra_die "ownership cannot prove interrupted same-boot restart"; return 1; fi; before="$current"; ra_state_jq '.scenarios[$n]=((.scenarios[$n]//{name:$n})+{private:((.scenarios[$n].private//{})+{before_pid:$pid,mutation_requested:true})})' --arg n "$scenario" --argjson pid "$before" || return 1; ra_capture systemctl restart "$RA_SERVICE" || return 1; ra_wait_new_pid "$before" >/dev/null || return 1; ra_wait_inactive 30 || return 1; fi; if [[ -n "$attempt_before" ]]; then attempt_after="$(ra_boot_attempt_snapshot)" || return 1; [[ "$(jq -cS . <<<"$attempt_before")" == "$(jq -cS . <<<"$attempt_after")" ]] || { ra_die "product same-boot restart replaced consumed boot-attempt authority"; return 1; }; fi; }
ra_successful_boot_restart_continuity() { local before current requested attempt_before attempt_after session_before session_after; attempt_before="$(ra_boot_attempt_snapshot)" || return 1; [[ "$(jq -r '.state' <<<"$attempt_before")" == succeeded ]] || { ra_die "product boot autostart attempt did not succeed exactly once"; return 1; }; session_before="$(ra_network_session_snapshot)" || return 1; before="$(jq -r '.scenarios.reboot_autostart_on.private.before_pid//""' "$RA_CHECKPOINT")"; requested="$(jq -r '.scenarios.reboot_autostart_on.private.mutation_requested//false' "$RA_CHECKPOINT")"; current="$(ra_main_pid)" || return 1; if [[ -n "$before" && "$current" != "$before" ]]; then ra_wait_active 30 || return 1; else if [[ -n "$before" && "$requested" == true ]]; then ra_die "ownership cannot prove interrupted boot restart"; return 1; fi; before="$current"; ra_state_jq '.scenarios.reboot_autostart_on=((.scenarios.reboot_autostart_on//{name:"reboot_autostart_on"})+{private:((.scenarios.reboot_autostart_on.private//{})+{before_pid:$pid,mutation_requested:true,attempt_before:$attempt,session_before:$session})})' --argjson pid "$before" --argjson attempt "$attempt_before" --argjson session "$session_before" || return 1; ra_privacy_watch_start reboot_active_restart; ra_capture systemctl restart "$RA_SERVICE" || { ra_privacy_watch_cancel; return 1; }; ra_wait_new_pid "$before" >/dev/null || return 1; ra_wait_active 120 || return 1; ra_privacy_watch_stop || return 1; fi; attempt_after="$(ra_boot_attempt_snapshot)" || return 1; session_after="$(ra_network_session_snapshot)" || return 1; [[ "$(jq -cS . <<<"$attempt_before")" == "$(jq -cS . <<<"$attempt_after")" ]] || { ra_die "product daemon restart admitted/replaced boot attempt"; return 1; }; [[ "$(jq -r '.session_id' <<<"$session_before")" == "$(jq -r '.session_id' <<<"$session_after")" ]] || { ra_die "product daemon restart changed Network Session"; return 1; }; ra_state_jq '.scenarios.reboot_autostart_on.private.attempt_after=$attempt|.scenarios.reboot_autostart_on.private.session_after=$session' --argjson attempt "$attempt_after" --argjson session "$session_after"; }

# Scenario/controller wiring.
ra_scenario_lower_upgrade() { local candidate profile cv installed before; candidate="$(jq -ce '.candidate' "$RA_CHECKPOINT")" || return 1; profile="$(jq -r '.private.selected_profile' "$RA_CHECKPOINT")"; cv="$(jq -r '.version' <<<"$candidate")"; installed="$(ra_pkg_installed_version)" || return 1; if [[ "$installed" == "$cv" ]]; then ra_wait_active 120 || return 1; ra_package_setup_release_after_candidate || return 1; ra_profile_validate "$profile" || return 1; ra_privacy_require_protected || return 1; ra_record lower_release_upgrade PASS; ra_record privacy_active PASS; return 0; fi; ra_connect "$profile" || return 1; ra_wait_active_legacy || return 1; installed="$(ra_pkg_installed_version)" || return 1; [[ "$installed" != "$cv" ]] || return 1; before="$(ra_main_pid)" || return 1; if ra_privacy_local_proof; then ra_record legacy_upgrade_privacy PASS; ra_privacy_watch_start lower_upgrade; fi; ra_candidate_upgrade_begin "$candidate" "$installed" || return 1; ra_pkg_install_exact "$candidate" || { ra_privacy_watch_cancel; return 1; }; ra_state_jq '.mutations.candidate_upgrade.identity.applied=true' || return 1; ra_mut_mark_acquired candidate_upgrade || return 1; ra_wait_new_pid "$before" >/dev/null || return 1; ra_wait_active || return 1; if [[ -n "$RA_PRIVACY_WATCH_PID" ]]; then ra_privacy_watch_stop || return 1; else ra_record legacy_upgrade_privacy SKIP_RELEASE_CAPABILITY "lower release has no Privacy Envelope evidence"; ra_privacy_require_protected || return 1; fi; ra_package_setup_release_after_candidate || return 1; ra_profile_validate "$profile" || return 1; ra_record lower_release_upgrade PASS || return 1; ra_privacy_require_protected || return 1; ra_record privacy_active PASS || return 1; ra_mut_begin_release candidate_upgrade || return 1; ra_mut_mark_released candidate_upgrade; }
ra_scenario_graceful_restart() { ra_lifecycle_graceful_restart || return 1; ra_record graceful_restart PASS; }
ra_scenario_daemon_kill() { ra_lifecycle_unexpected_death || return 1; ra_record daemon_kill PASS; }
ra_scenario_rollback() { local profile; profile="$(jq -r '.private.selected_profile' "$RA_CHECKPOINT")"; ra_lifecycle_rollback_interruption "$profile" || return 1; ra_record rollback_interruption PASS; }
ra_scenario_stop_start() { ra_lifecycle_stop_start || return 1; ra_record stop_start_no_reconnect PASS; }
ra_scenario_reinstall() { local candidate profile observed; candidate="$(jq -ce '.candidate' "$RA_CHECKPOINT")"; profile="$(jq -r '.private.selected_profile' "$RA_CHECKPOINT")"; observed="$(ra_session_observe)" || return 1; if [[ "$observed" == inactive ]]; then ra_connect "$profile" || return 1; elif [[ "$observed" != active ]]; then return 1; fi; ra_wait_active || return 1; ra_lifecycle_reinstall "$candidate" || return 1; ra_record reinstall PASS; }
ra_scenario_warmed_baseline() { local inactive; ra_disconnect || return 1; ra_wait_inactive || return 1; ra_privacy_require_ordinary || return 1; if [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" != 1 ]]; then sleep 5; fi; inactive="$(ra_inactive_sample warmed_inactive)" || return 1; ra_state_jq '.private.warmed_inactive=$v' --argjson v "$inactive" || return 1; ra_record warmed_inactive_candidate_baseline PASS; }
ra_scenario_preconnect_coexistence() { local profile; profile="$(jq -r '.private.selected_profile' "$RA_CHECKPOINT")"; ra_fixture_acquire fixture_a || return 1; ra_connect "$profile" || return 1; ra_wait_active || return 1; ra_privacy_require_protected || return 1; ra_collision_require_disjoint fixture_a || return 1; ra_record preconnect_coexistence PASS; }
ra_scenario_resource_soak() { ra_soak_run; }
ra_scenario_disconnect_cleanup() { local profile post; profile="$(jq -r '.private.selected_profile' "$RA_CHECKPOINT")"; ra_disconnect || return 1; ra_wait_inactive || return 1; ra_privacy_require_ordinary || return 1; if [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" != 1 ]]; then sleep 5; fi; post="$(ra_inactive_sample post_disconnect)" || return 1; ra_state_jq '.private.resource.post_disconnect=$post' --argjson post "$post" || return 1; ra_fixture_verify fixture_a || return 1; ra_record disconnect_cleanup PASS; ra_connect "$profile" || return 1; ra_wait_active || return 1; ra_collision_require_disjoint fixture_a || return 1; ra_record coexistence_reconnect PASS; ra_second_session_observe || return 1; ra_disconnect || return 1; ra_wait_inactive || return 1; ra_privacy_require_ordinary || return 1; ra_fixture_release fixture_a; }
ra_scenario_terminal() { local profile; profile="$(jq -r '.private.selected_profile' "$RA_CHECKPOINT")"; ra_runtime_terminal_failure "$profile" || return 1; ra_record runtime_terminal_convergence PASS; ra_record runtime_terminal_no_retry PASS; }

ra_reconcile_scenario_mutations() { local scenario="$1" entries name kind state; entries="$(jq -r --arg s "$scenario" '.mutations|to_entries|reverse[]|select((.value.scenario//"")==$s and .value.state!="released")|[.key,.value.kind,.value.state]|@tsv' "$RA_CHECKPOINT")" || return 1; while IFS=$'\t' read -r name kind state; do [[ -n "$name" ]] || continue; case "$kind" in network_fixture) ra_fixture_release "$name" || return 1 ;; systemd_dropin) ra_systemd_hook_cleanup "$name" || return 1 ;; networkmanager_connection) ra_nm_reconcile "$name" || return 1 ;; candidate_package) case "$name" in candidate_upgrade) ra_candidate_upgrade_reconcile_cleanup || return 1 ;; reinstall_package) ra_reinstall_package_reconcile_cleanup || return 1 ;; *) return 1 ;; esac ;; autostart_policy) ra_autostart_release_owned "$name" || return 1 ;; *) ra_die "internal cannot reconcile scenario mutation $name/$kind"; return 1 ;; esac; done <<<"$entries"; }

ra_recover_running_scenario() {
  local name="$1" before current observed stop_requested stop_completed start_requested start_completed service_rc fixture_state profile state installed cv upgrade_before requested
  case "$name" in
    graceful_restart|daemon_kill)
      ra_privacy_watch_cancel
      before="$(jq -r --arg n "$name" '.scenarios[$n].private.before_pid//""' "$RA_CHECKPOINT")" || return 1
      requested="$(jq -r --arg n "$name" '.scenarios[$n].private.mutation_requested//false' "$RA_CHECKPOINT")" || return 1
      if [[ -n "$before" ]]; then
        current="$(ra_main_pid 2>/dev/null || true)"
        [[ -n "$current" ]] || { ra_die "host_state $name daemon pid unavailable during replay"; return 1; }
        if [[ "$current" != "$before" ]]; then
          ra_wait_active 30 || return 1
          ra_privacy_require_protected || return 1
          ra_record "$name" PASS || return 1
          ra_scenario_set_state "$name" passed
          return $?
        fi
        if [[ "$requested" == true ]]; then ra_die "ownership cannot prove whether interrupted $name mutation ran; refusing blind replay"; return 1; fi
      fi
      ra_scenario_set_state "$name" prepared
      ;;
    stop_start_no_reconnect)
      stop_requested="$(jq -r '.scenarios.stop_start_no_reconnect.private.stop_requested//false' "$RA_CHECKPOINT")"
      stop_completed="$(jq -r '.scenarios.stop_start_no_reconnect.private.stop_completed//false' "$RA_CHECKPOINT")"
      start_requested="$(jq -r '.scenarios.stop_start_no_reconnect.private.start_requested//false' "$RA_CHECKPOINT")"
      start_completed="$(jq -r '.scenarios.stop_start_no_reconnect.private.start_completed//false' "$RA_CHECKPOINT")"
      if [[ "$stop_requested" == true && "$stop_completed" != true ]]; then
        if ra_service_is_active; then ra_die "ownership cannot prove whether interrupted service stop ran; refusing blind stop replay"; return 1; else service_rc=$?; [[ "$service_rc" == 1 ]] || return 1; ra_state_jq '.scenarios.stop_start_no_reconnect.private.stop_completed=true' || return 1; stop_completed=true; fi
      fi
      if [[ "$stop_completed" == true ]]; then
        if [[ "$start_completed" == true ]]; then
          ra_service_is_active || { service_rc=$?; [[ "$service_rc" == 1 ]] && ra_die "host_state service stopped after completed restart boundary"; return 1; }
          ra_verify_inactive_boundary || return 1
          ra_privacy_require_ordinary || return 1
          ra_record stop_start_no_reconnect PASS
          ra_scenario_set_state "$name" passed
          return 0
        fi
        if [[ "$start_requested" == true ]]; then
          if ra_service_is_active; then
            ra_state_jq '.scenarios.stop_start_no_reconnect.private.start_completed=true' || return 1
            ra_verify_inactive_boundary || return 1
            ra_privacy_require_ordinary || return 1
            ra_record stop_start_no_reconnect PASS
            ra_scenario_set_state "$name" passed
            return 0
          fi
          service_rc=$?
          [[ "$service_rc" == 1 ]] || return 1
          ra_die "ownership cannot prove whether interrupted service start ran; refusing blind start replay"
          return 1
        fi
        ra_verify_inactive_boundary || return 1
        ra_privacy_require_ordinary || return 1
        ra_state_jq '.scenarios.stop_start_no_reconnect.private.start_requested=true' || return 1
        ra_capture systemctl start "$RA_SERVICE" || return 1
        ra_state_jq '.scenarios.stop_start_no_reconnect.private.start_completed=true' || return 1
        ra_wait_inactive 90 || return 1
        ra_privacy_require_ordinary || return 1
        ra_record stop_start_no_reconnect PASS
        ra_scenario_set_state "$name" passed
        return 0
      fi
      ra_scenario_set_state "$name" prepared
      ;;
    lower_release_upgrade)
      if jq -e '.mutations.candidate_upgrade and .mutations.candidate_upgrade.state!="released"' "$RA_CHECKPOINT" >/dev/null 2>&1; then
        state="$(jq -r '.mutations.candidate_upgrade.state' "$RA_CHECKPOINT")" || return 1
        installed="$(ra_pkg_installed_version)" || return 1
        cv="$(jq -r '.candidate.version' "$RA_CHECKPOINT")" || return 1
        upgrade_before="$(jq -r '.mutations.candidate_upgrade.identity.installed_before//""' "$RA_CHECKPOINT")" || return 1
        if [[ "$state" == acquiring && "$installed" == "$upgrade_before" ]]; then ra_die "ownership cannot prove whether interrupted candidate upgrade ran; refusing blind package replay"; return 1; fi
        if [[ "$installed" == "$cv" ]]; then
          ra_candidate_upgrade_reconcile_cleanup || return 1
          ra_die "ownership lower-release upgrade evidence interrupted after candidate installation; restart required"
          return 1
        fi
        ra_die "ownership candidate upgrade package state ambiguous during replay"
        return 1
      fi
      observed="$(ra_session_observe)"
      if [[ "$observed" == active ]]; then ra_disconnect || return 1; ra_wait_inactive 120 || return 1; elif [[ "$observed" != inactive ]]; then return 1; fi
      ra_reconcile_scenario_mutations "$name" || return 1
      ra_scenario_set_state "$name" prepared
      ;;
    reinstall)
      if jq -e '.mutations.reinstall_package and .mutations.reinstall_package.state!="released"' "$RA_CHECKPOINT" >/dev/null 2>&1; then
        ra_reinstall_package_reconcile_cleanup || return 1
        ra_wait_active 30 || return 1
        ra_privacy_require_protected || return 1
        ra_record reinstall PASS || return 1
        ra_scenario_set_state "$name" passed
        return $?
      fi
      if [[ "$(jq -r '.scenarios.reinstall.private.dpkg_completed//false' "$RA_CHECKPOINT")" == true ]]; then
        before="$(jq -r '.scenarios.reinstall.private.before_pid//""' "$RA_CHECKPOINT")" || return 1
        current="$(ra_main_pid 2>/dev/null || true)"
        [[ -n "$before" && -n "$current" && "$current" != "$before" ]] || { ra_die "ownership cannot prove completed same-candidate reinstall after interruption"; return 1; }
        ra_wait_active 30 || return 1
        ra_privacy_require_protected || return 1
        ra_record reinstall PASS || return 1
        ra_scenario_set_state "$name" passed
        return $?
      fi
      ra_scenario_set_state "$name" prepared
      ;;
    disconnect_cleanup)
      fixture_state="$(jq -r '.mutations.fixture_a.state//"released"' "$RA_CHECKPOINT")"
      if [[ "$fixture_state" == released && "$(jq -r '.scenarios.disconnect_cleanup.outcome//""' "$RA_CHECKPOINT")" == PASS && "$(jq -r '.scenarios.coexistence_reconnect.outcome//""' "$RA_CHECKPOINT")" == PASS && "$(jq -r '.scenarios.reconnect_resource_nonaccumulation.outcome//""' "$RA_CHECKPOINT")" == PASS ]]; then
        ra_verify_inactive_boundary || return 1
        ra_privacy_require_ordinary || return 1
        ra_scenario_set_state "$name" passed
        return 0
      fi
      [[ "$fixture_state" != released ]] || { ra_die "ownership disconnect cleanup lost fixture boundary before evidence completed"; return 1; }
      observed="$(ra_session_observe)"
      if [[ "$observed" == inactive ]]; then
        profile="$(jq -r '.private.selected_profile' "$RA_CHECKPOINT")"
        ra_connect "$profile" || return 1
        ra_wait_active || return 1
        ra_collision_require_disjoint fixture_a || return 1
      elif [[ "$observed" != active ]]; then return 1; fi
      ra_scenario_set_state "$name" prepared
      ;;
    rollback_interruption|preconnect_coexistence|resource_soak|runtime_terminal_convergence|warmed_inactive_candidate_baseline)
      ra_privacy_watch_cancel
      if [[ "$name" == preconnect_coexistence || "$name" == rollback_interruption || "$name" == runtime_terminal_convergence || "$name" == warmed_inactive_candidate_baseline ]]; then
        observed="$(ra_session_observe)"
        if [[ "$observed" == active ]]; then ra_disconnect || return 1; ra_wait_inactive 120 || return 1; elif [[ "$observed" != inactive ]]; then return 1; fi
      fi
      ra_reconcile_scenario_mutations "$name" || return 1
      ra_scenario_set_state "$name" prepared
      ;;
    *) ra_reconcile_scenario_mutations "$name" || return 1; ra_scenario_set_state "$name" prepared ;;
  esac
}

ra_finish_verifying_scenario() {
  local name="$1"
  case "$name" in
    lower_release_upgrade)
      [[ "$(jq -r '.scenarios.lower_release_upgrade.outcome//""' "$RA_CHECKPOINT")" == PASS ]] || return 1
      [[ "$(jq -r '.scenarios.privacy_active.outcome//""' "$RA_CHECKPOINT")" == PASS ]] || return 1
      ra_wait_active 30 || return 1
      ra_privacy_require_protected || return 1
      ;;
    graceful_restart|daemon_kill|reinstall|preconnect_coexistence|resource_soak)
      ra_wait_active 30 || return 1
      ra_privacy_require_protected || return 1
      ;;
    warmed_inactive_candidate_baseline|stop_start_no_reconnect|runtime_terminal_convergence)
      ra_verify_inactive_boundary || return 1
      ;;
    disconnect_cleanup)
      ra_verify_inactive_boundary || return 1
      ra_privacy_require_ordinary || return 1
      ;;
    rollback_interruption) ra_wait_active 30 || return 1; ra_privacy_require_protected || return 1 ;;
    *) ;;
  esac
  ra_scenario_set_state "$name" passed || return 1
  ra_scenario_clear_current || return 1
  RA_CURRENT_SCENARIO=""
}

ra_scenario_run() {
  local name="$1" action="$2" state
  shift 2
  RA_CURRENT_SCENARIO="$name"
  state="$(jq -r --arg n "$name" '.scenarios[$n].state//""' "$RA_CHECKPOINT")" || return 1
  case "$state" in
    passed) ra_scenario_clear_current || return 1; RA_CURRENT_SCENARIO=""; return 0 ;;
    failed) ra_die "product scenario $name already failed"; return 1 ;;
    verifying) ra_finish_verifying_scenario "$name"; return $? ;;
    running)
      ra_recover_running_scenario "$name" || { ra_scenario_set_state "$name" failed || true; return 1; }
      state="$(jq -r --arg n "$name" '.scenarios[$n].state//""' "$RA_CHECKPOINT")"
      if [[ "$state" == passed ]]; then ra_scenario_clear_current || return 1; RA_CURRENT_SCENARIO=""; return 0; fi
      if [[ "$state" == verifying ]]; then ra_finish_verifying_scenario "$name"; return $?; fi
      ;;
    "") ra_scenario_set_state "$name" pending || return 1 ;;
    pending|prepared) ;;
    *) ra_die "internal unsupported scenario state $name/$state"; return 1 ;;
  esac
  state="$(jq -r --arg n "$name" '.scenarios[$n].state//""' "$RA_CHECKPOINT")"
  [[ "$state" == prepared ]] || ra_scenario_set_state "$name" prepared || return 1
  ra_scenario_set_state "$name" running || return 1
  if ! "$action" "$@"; then ra_scenario_set_state "$name" failed || true; return 1; fi
  ra_scenario_set_state "$name" verifying || return 1
  ra_finish_verifying_scenario "$name"
}

ra_run_pre_reboot() { ra_set_phase running-pre-reboot || return 1; ra_scenario_run lower_release_upgrade ra_scenario_lower_upgrade || return 1; ra_scenario_run graceful_restart ra_scenario_graceful_restart || return 1; ra_scenario_run daemon_kill ra_scenario_daemon_kill || return 1; ra_scenario_run rollback_interruption ra_scenario_rollback || return 1; ra_scenario_run stop_start_no_reconnect ra_scenario_stop_start || return 1; ra_scenario_run reinstall ra_scenario_reinstall || return 1; ra_scenario_run warmed_inactive_candidate_baseline ra_scenario_warmed_baseline || return 1; ra_scenario_run preconnect_coexistence ra_scenario_preconnect_coexistence || return 1; ra_scenario_run resource_soak ra_scenario_resource_soak || return 1; ra_scenario_run disconnect_cleanup ra_scenario_disconnect_cleanup || return 1; ra_scenario_run runtime_terminal_convergence ra_scenario_terminal || return 1; if ((RA_REBOOT_PHASES==0)); then ra_record reboot_autostart_off SKIP_USER_REQUEST; ra_record reboot_autostart_on SKIP_USER_REQUEST; ra_record explicit_disconnect_no_same_boot_retry SKIP_USER_REQUEST; ra_record reboot_terminal_autostart SKIP_USER_REQUEST; ra_record terminal_no_same_boot_retry SKIP_USER_REQUEST; ra_finalize; else ra_prepare_reboot_off; fi; }
ra_prepare_reboot_off() { local boot; boot="$(ra_boot_id)" || return 1; ra_state_jq '.previous_boot_id=$boot|.current_boot_id=$boot|.phase="preparing-reboot-autostart-off"' --arg boot "$boot" || return 1; RA_CURRENT_SCENARIO=reboot_autostart_off; ra_autostart_ensure_owned autostart_disable disable || return 1; RA_CURRENT_SCENARIO=""; ra_set_phase await-reboot-autostart-off || return 1; printf 'Release acceptance checkpoint saved.\nReboot the laptop, then run: sudo ./release-laptop.sh --resume\n'; }
ra_prepare_reboot_on() { local profile current; profile="$(jq -r '.private.selected_profile//""' "$RA_CHECKPOINT")"; current="$(ra_boot_id)" || return 1; ra_state_jq '.previous_boot_id=$boot|.current_boot_id=$boot|.phase="preparing-reboot-autostart-on"' --arg boot "$current" || return 1; RA_CURRENT_SCENARIO=reboot_autostart_on; ra_autostart_ensure_owned autostart_enable enable "$profile" || return 1; RA_CURRENT_SCENARIO=""; ra_set_phase await-reboot-autostart-on || return 1; printf 'Release acceptance checkpoint advanced.\nReboot the laptop again, then run: sudo ./release-laptop.sh --resume\n'; }
ra_prepare_reboot_terminal() { local terminal_id current; current="$(ra_boot_id)" || return 1; ra_state_jq '.previous_boot_id=$boot|.current_boot_id=$boot|.phase="preparing-reboot-terminal"' --arg boot "$current" || return 1; terminal_id="$(ra_terminal_profile_ensure)" || return 1; RA_CURRENT_SCENARIO=reboot_terminal_autostart; ra_autostart_ensure_owned autostart_terminal enable "$terminal_id" || return 1; RA_CURRENT_SCENARIO=""; ra_set_phase await-reboot-terminal || return 1; printf 'Release acceptance checkpoint advanced.\nReboot the laptop again, then run: sudo ./release-laptop.sh --resume\n'; }
ra_resume_require_new_boot() { local old current; old="$(jq -r '.previous_boot_id' "$RA_CHECKPOINT")"; current="$(ra_boot_id)"; [[ -n "$old" && "$current" != "$old" ]] || { ra_die "host_state --resume requires a real reboot"; return 1; }; ra_state_jq '.current_boot_id=$boot' --arg boot "$current"; }
ra_resume_verify_candidate() { local candidate installed; candidate="$(jq -ce '.candidate' "$RA_CHECKPOINT")" || return 1; installed="$(ra_pkg_installed_version)" || return 1; [[ "$installed" == "$(jq -r '.version' <<<"$candidate")" ]] || { ra_die "product installed candidate changed across reboot"; return 1; }; }
ra_resume_reboot_off_verify() { if [[ "$(jq -r '.scenarios.reboot_autostart_off.outcome//""' "$RA_CHECKPOINT")" != PASS ]]; then ra_resume_verify_candidate || return 1; ra_wait_inactive 120 || return 1; ra_verify_ordinary_network || return 1; ra_boot_attempt_assert_absent || { ra_die "product autostart-disabled boot admitted an attempt"; return 1; }; ra_record reboot_autostart_off PASS || return 1; fi; ra_prepare_reboot_on; }
ra_resume_reboot_on_verify() { ra_resume_verify_candidate || return 1; if [[ "$(jq -r '.scenarios.reboot_autostart_on.outcome//""' "$RA_CHECKPOINT")" != PASS ]]; then ra_wait_active 180 || return 1; ra_privacy_require_protected || return 1; ra_successful_boot_restart_continuity || return 1; ra_record reboot_autostart_on PASS || return 1; fi; if [[ "$(jq -r '.scenarios.explicit_disconnect_no_same_boot_retry.outcome//""' "$RA_CHECKPOINT")" != PASS ]]; then ra_safe_disconnect_if_owned || return 1; ra_privacy_require_ordinary || return 1; ra_same_boot_restart_stays_inactive explicit_disconnect_no_same_boot_retry || return 1; ra_record explicit_disconnect_no_same_boot_retry PASS || return 1; fi; ra_prepare_reboot_terminal; }
ra_resume_reboot_terminal_verify() { ra_resume_verify_candidate || return 1; local attempt reason; if [[ "$(jq -r '.scenarios.reboot_terminal_autostart.outcome//""' "$RA_CHECKPOINT")" != PASS ]]; then ra_wait_inactive 180 || return 1; attempt="$(ra_boot_attempt_snapshot)" || return 1; [[ "$(jq -r '.state' <<<"$attempt")" == terminal ]] || return 1; reason="$(jq -r '.terminal_reason//""' <<<"$attempt")"; [[ "$reason" == connect_failed ]] || return 1; ra_privacy_require_ordinary || return 1; ra_state_jq '.scenarios.reboot_terminal_autostart.private.attempt=$attempt' --argjson attempt "$attempt" || return 1; ra_record reboot_terminal_autostart PASS "" "$(jq -c '{state,terminal_reason}' <<<"$attempt")" || return 1; fi; if [[ "$(jq -r '.scenarios.terminal_no_same_boot_retry.outcome//""' "$RA_CHECKPOINT")" != PASS ]]; then ra_same_boot_restart_stays_inactive terminal_no_same_boot_retry || return 1; attempt="$(ra_boot_attempt_snapshot)" || return 1; [[ "$(jq -r '.state' <<<"$attempt")" == terminal ]] || return 1; ra_record terminal_no_same_boot_retry PASS || return 1; fi; ra_finalize; }

ra_preflight_capabilities() { if [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" != 1 ]]; then [[ -r /sys/fs/cgroup/cgroup.controllers ]] || { ra_preflight_die "cgroup v2 accounting is required"; return 2; }; fi; [[ ! -e "$RA_ROLLBACK_OVERRIDE" && ! -L "$RA_ROLLBACK_OVERRIDE" && ! -e "$RA_ROLLBACK_HOOK_DIR" && ! -L "$RA_ROLLBACK_HOOK_DIR" ]] || { ra_preflight_die "rollback fault-injection identity occupied"; return 2; }; [[ ! -e "$RA_TERMINAL_OVERRIDE" && ! -L "$RA_TERMINAL_OVERRIDE" && ! -e "$RA_TERMINAL_HOOK_DIR" && ! -L "$RA_TERMINAL_HOOK_DIR" ]] || { ra_preflight_die "terminal fault-injection identity occupied"; return 2; }; local spec; spec="$(ra_fixture_spec fixture_a)" || return 2; ra_fixture_assert_free "$spec" || { ra_preflight_die "fixture_a identity not free"; return 2; }; spec="$(ra_fixture_spec fixture_b)" || return 2; ra_fixture_assert_free "$spec" || { ra_preflight_die "fixture_b identity not free"; return 2; }; }

ra_existing_checkpoint_classify() { ra_checkpoint_exists >/dev/null 2>&1 || { printf none; return 0; }; if ! ra_state_require_schema >/dev/null 2>&1; then printf ambiguous; return 0; fi; local phase state current; phase="$(jq -r '.phase//""' "$RA_CHECKPOINT")"; case "$phase" in await-reboot-autostart-off|await-reboot-autostart-on|await-reboot-terminal) printf reboot-wait ;; fail-cleanup-failed) printf ambiguous ;; failure-cleanup-running|failed-cleanable|scenario-failed) printf cleanup-restart ;; preparing-lower-release|preparing-reboot-autostart-off|preparing-reboot-autostart-on|preparing-reboot-terminal|verifying-reboot-autostart-off|verifying-reboot-autostart-on|verifying-reboot-terminal) printf replay-safe ;; running-pre-reboot) current="$(jq -r '.current_scenario//""' "$RA_CHECKPOINT")"; if [[ -z "$current" ]]; then printf replay-safe; else state="$(jq -r --arg n "$current" '.scenarios[$n].state//""' "$RA_CHECKPOINT")"; case "$state" in pending|prepared|"") printf replay-safe ;; running) case "$current" in graceful_restart|daemon_kill|stop_start_no_reconnect|lower_release_upgrade|reinstall) printf replay-safe ;; rollback_interruption|warmed_inactive_candidate_baseline|preconnect_coexistence|resource_soak|disconnect_cleanup|runtime_terminal_convergence) printf cleanup-restart ;; *) printf ambiguous ;; esac ;; verifying|passed) printf replay-safe ;; failed) printf cleanup-restart ;; *) printf ambiguous ;; esac; fi ;; complete|aborted-clean|failed-clean|restarted-clean|restarted_clean) printf cleanup-restart ;; *) printf ambiguous ;; esac; }
ra_checkpoint_candidate_rebind() { local candidate="$1"; jq -e --argjson c "$candidate" '.candidate.package==$c.package and .candidate.version==$c.version and .candidate.architecture==$c.architecture and .candidate.sha256==$c.sha256' "$RA_CHECKPOINT" >/dev/null || return 1; ra_state_jq '.candidate=$c|if .mutations.package_setup then .mutations.package_setup.identity.candidate=$c else . end|if .mutations.candidate_upgrade then .mutations.candidate_upgrade.identity.candidate=$c else . end' --argjson c "$candidate"; }
ra_checkpoint_run_config_compatible() { local profile soak wifi suspend reboots; profile="$(jq -r '.private.selected_profile//.private.run_config.profile//""' "$RA_CHECKPOINT")"; soak="$(jq -r '.private.run_config.soak_minutes//60' "$RA_CHECKPOINT")"; wifi="$(jq -r 'if .private.run_config.allow_wifi_reconnect then 1 else 0 end' "$RA_CHECKPOINT")"; suspend="$(jq -r 'if .private.run_config.allow_suspend then 1 else 0 end' "$RA_CHECKPOINT")"; reboots="$(jq -r 'if .private.run_config.reboot_phases then 1 else 0 end' "$RA_CHECKPOINT")"; [[ -z "$RA_PROFILE" || "$RA_PROFILE" == "$profile" ]] || return 1; [[ "$RA_SOAK_MINUTES" == "$soak" && "$RA_ALLOW_WIFI" == "$wifi" && "$RA_ALLOW_SUSPEND" == "$suspend" && "$RA_REBOOT_PHASES" == "$reboots" ]] || return 1; if ((RA_ARTIFACT_DIR_EXPLICIT==1)); then [[ "$(readlink -m "$RA_ARTIFACT_DIR")" == "$(readlink -m "$(jq -r '.private.artifact_root' "$RA_CHECKPOINT")")" ]] || return 1; fi; }
ra_preflight_new_inputs_before_retire() { local candidate previous="" installed ids id terminal valid_count=0; candidate="$(ra_pkg_inspect "$RA_CANDIDATE")" || return $?; [[ -z "$RA_PREVIOUS_DEB" ]] || previous="$(ra_pkg_inspect "$RA_PREVIOUS_DEB")" || return $?; installed="$(ra_cleanup_expected_package_version)" || return 1; ra_preflight_release_boundary "$candidate" "$previous" "$installed" || return $?; ra_candidate_fault_seams_verify "$candidate" || return $?; ra_validate_artifact_root "$RA_ARTIFACT_DIR" || return $?; terminal="$(jq -r '.private.terminal_profile//""' "$RA_CHECKPOINT" 2>/dev/null || true)"; if [[ -n "$RA_PROFILE" ]]; then [[ -z "$terminal" || "$RA_PROFILE" != "$terminal" ]] || return 2; ra_profile_validate "$RA_PROFILE" || return 2; return 0; fi; ids="$(ra_profile_ids_json)" || return 2; while IFS= read -r id; do [[ -n "$id" && "$id" != "$terminal" ]] || continue; if ra_profile_validate "$id"; then ((valid_count+=1)); fi; done < <(jq -r '.[]' <<<"$ids"); ((valid_count==1)) || return 2; }
ra_run_new_fresh() { local candidate previous="" installed manifest run_id baseline profile; candidate="$(ra_pkg_inspect "$RA_CANDIDATE")" || return $?; [[ -z "$RA_PREVIOUS_DEB" ]] || previous="$(ra_pkg_inspect "$RA_PREVIOUS_DEB")" || return $?; installed="$(ra_pkg_installed_version)" || return 1; ra_preflight_release_boundary "$candidate" "$previous" "$installed" || return $?; ra_candidate_fault_seams_verify "$candidate" || return $?; ra_preflight_clean_boundary "$installed" || return $?; ra_validate_artifact_root "$RA_ARTIFACT_DIR" || return $?; profile="$(ra_profile_select "$RA_PROFILE")" || return 2; ra_preflight_capabilities || return $?; manifest="$(ra_boot_manifest_capture)" || return 2; baseline="$(ra_privacy_baseline)" || return 2; run_id="$(date -u +%Y%m%dT%H%M%SZ)-$$"; ra_artifacts_init_new "$run_id" || return $?; ra_state_init "$run_id" "$candidate" "$installed" "$manifest" "$profile" || return 1; ra_state_jq '.private.privacy_baseline=$baseline|.private.selected_profile=$profile|.private.run_config.previous=$previous' --argjson baseline "$baseline" --arg profile "$profile" --argjson previous "${previous:-null}" || return 1; ra_package_setup_prepare "$candidate" "$previous" "$installed" || return $?; ra_run_pre_reboot; }
ra_retire_existing_run() { local result="$1" phase; ra_artifacts_from_state || return 1; ra_failure_bundle_capture "retire_${result}" 1 restart || true; if ! ra_safe_cleanup; then ra_state_jq '.phase="fail-cleanup-failed"' || true; ra_report_write FAIL_CLEANUP_FAILED || true; printf 'FAIL_CLEANUP_FAILED\n'; return 1; fi; phase="${result,,}"; phase="${phase//_/-}"; ra_set_phase "$phase" || return 1; ra_report_write "$result" || return 1; ra_verify_run_tree || return 1; ra_state_remove; }
ra_run_new_current() { local classification candidate rc; if ! ra_checkpoint_exists >/dev/null 2>&1; then ra_run_new_fresh; return $?; fi; classification="$(ra_existing_checkpoint_classify)"; case "$classification" in reboot-wait) printf 'PAUSED: reboot required\nNext: sudo ./release-laptop.sh --resume\nTo discard this reboot evidence explicitly: sudo ./release-laptop.sh %q --restart\n' "$RA_CANDIDATE"; return "$RA_RC_PAUSED" ;; ambiguous) RA_SUPPRESS_FINALIZER=1; ra_die "ownership existing checkpoint ambiguous"; return 1 ;; replay-safe) if candidate="$(ra_pkg_inspect "$RA_CANDIDATE")"; then :; else rc=$?; RA_SUPPRESS_FINALIZER=1; return "$rc"; fi; ra_checkpoint_run_config_compatible || { RA_SUPPRESS_FINALIZER=1; return 1; }; ra_checkpoint_candidate_rebind "$candidate" || { RA_SUPPRESS_FINALIZER=1; return 1; }; ra_artifacts_from_state || return 1; ra_run_resume ;; cleanup-restart) if ra_preflight_new_inputs_before_retire; then :; else rc=$?; RA_SUPPRESS_FINALIZER=1; return "$rc"; fi; ra_retire_existing_run RESTARTED_CLEAN || return 1; ra_run_new_fresh ;; *) return 1 ;; esac; }
ra_resume_load_config() { RA_SOAK_MINUTES="$(jq -r '.private.run_config.soak_minutes//60' "$RA_CHECKPOINT")"; RA_ALLOW_WIFI="$(jq -r 'if .private.run_config.allow_wifi_reconnect then 1 else 0 end' "$RA_CHECKPOINT")"; RA_ALLOW_SUSPEND="$(jq -r 'if .private.run_config.allow_suspend then 1 else 0 end' "$RA_CHECKPOINT")"; RA_REBOOT_PHASES="$(jq -r 'if .private.run_config.reboot_phases then 1 else 0 end' "$RA_CHECKPOINT")"; }
ra_run_resume_current() { ra_checkpoint_exists || return 2; ra_state_require_schema || { RA_SUPPRESS_FINALIZER=1; return 1; }; ra_artifacts_from_state || { RA_SUPPRESS_FINALIZER=1; return 1; }; ra_resume_load_config || return 1; local phase candidate previous; phase="$(jq -r '.phase' "$RA_CHECKPOINT")" || return 1; case "$phase" in preparing-lower-release) candidate="$(jq -ce '.candidate' "$RA_CHECKPOINT")"; previous="$(jq -ce '.private.run_config.previous//empty' "$RA_CHECKPOINT" 2>/dev/null || true)"; ra_package_setup_resume_prepare "$candidate" "$previous" || return 1; ra_run_pre_reboot ;; running-pre-reboot) ra_run_pre_reboot ;; preparing-reboot-autostart-off) ra_prepare_reboot_off ;; await-reboot-autostart-off) ra_resume_require_new_boot || return 1; ra_set_phase verifying-reboot-autostart-off || return 1; ra_resume_reboot_off_verify ;; verifying-reboot-autostart-off) ra_resume_reboot_off_verify ;; preparing-reboot-autostart-on) ra_prepare_reboot_on ;; await-reboot-autostart-on) ra_resume_require_new_boot || return 1; ra_set_phase verifying-reboot-autostart-on || return 1; ra_resume_reboot_on_verify ;; verifying-reboot-autostart-on) ra_resume_reboot_on_verify ;; preparing-reboot-terminal) ra_prepare_reboot_terminal ;; await-reboot-terminal) ra_resume_require_new_boot || return 1; ra_set_phase verifying-reboot-terminal || return 1; ra_resume_reboot_terminal_verify ;; verifying-reboot-terminal) ra_resume_reboot_terminal_verify ;; *) RA_SUPPRESS_FINALIZER=1; return 1 ;; esac; }
ra_run_abort_current() { ra_checkpoint_exists || return 2; ra_state_require_schema || return 1; ra_artifacts_from_state || return 1; ra_failure_bundle_capture explicit_abort 1 abort || true; if ! ra_safe_cleanup; then ra_set_phase fail-cleanup-failed || true; ra_report_write FAIL_CLEANUP_FAILED || true; printf 'FAIL_CLEANUP_FAILED\n'; return 1; fi; ra_set_phase aborted-clean || return 1; ra_report_write ABORTED_CLEAN || return 1; ra_verify_run_tree || return 1; ra_state_remove || return 1; printf 'ABORTED_CLEAN\n'; }
ra_run_restart_current() { local rc; if ra_checkpoint_exists >/dev/null 2>&1; then ra_state_require_schema || { RA_SUPPRESS_FINALIZER=1; return 1; }; if ra_preflight_new_inputs_before_retire; then :; else rc=$?; RA_SUPPRESS_FINALIZER=1; return "$rc"; fi; ra_retire_existing_run RESTARTED_CLEAN || return 1; fi; RA_MODE=new; ra_run_new_fresh; }
ra_phase_blocks_auto_finalizer() { local phase; phase="$(jq -r '.phase//""' "$RA_CHECKPOINT" 2>/dev/null || true)"; case "$phase" in await-reboot-autostart-off|await-reboot-autostart-on|await-reboot-terminal|fail-cleanup-failed|failed-clean|aborted-clean|restarted-clean|restarted_clean|complete) return 0 ;; *) return 1 ;; esac; }
