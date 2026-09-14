ra_product() { if [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" == 1 ]]; then ra_capture podlaz "$@"; else ra_capture_user /usr/bin/podlaz "$@"; fi; }
ra_profile_ids_json() { ra_product profile list --json || return 1; jq -ce 'select(.schema_version=="v1")|[.profiles[]?|select(type=="object" and .id)|.id]' <<<"$RA_CAPTURE"; }
ra_profile_validate() { ra_product profile validate "$1" --mode tun --json || return 1; jq -e '.schema_version=="v1" and .valid==true' <<<"$RA_CAPTURE" >/dev/null; }
ra_profile_select() { local explicit="$1" ids id; local valid=(); ids="$(ra_profile_ids_json)" || return 1; if [[ -n "$explicit" ]]; then jq -e --arg id "$explicit" 'index($id)!=null' <<<"$ids" >/dev/null || return 1; ra_profile_validate "$explicit" || return 1; printf '%s' "$explicit"; return 0; fi; while IFS= read -r id; do if [[ -n "$id" ]] && ra_profile_validate "$id"; then valid+=("$id"); fi; done < <(jq -r '.[]' <<<"$ids"); ((${#valid[@]}==1)) || { ra_die "input expected exactly one usable TUN profile, found ${#valid[@]}"; return 1; }; printf '%s' "${valid[0]}"; }
ra_connect() { ra_product connect --mode tun "$1" >/dev/null || { ra_die "product Podlaz TUN connect failed"; return 1; }; }
ra_disconnect() { ra_product disconnect >/dev/null || { ra_die "product Podlaz disconnect failed"; return 1; }; }

ra_status_json() {
  local payload
  ra_capture curl --fail --silent --max-time 5 --unix-socket "$RA_SOCKET" http://localhost/v1/status || return 1
  if [[ -n "$RA_PRIVATE_DIR" && -d "$RA_PRIVATE_DIR" ]]; then
    printf '%s\n' "$RA_CAPTURE" | ra_artifact_file_write "$RA_PRIVATE_DIR/last-status-observed.txt" 2>/dev/null || true
  fi
  if ! payload="$(jq -ce . <<<"$RA_CAPTURE" 2>/dev/null)"; then
    ra_die "status_contract_incompatible daemon status is not JSON"
    return 2
  fi
  if [[ -n "$RA_PRIVATE_DIR" && -d "$RA_PRIVATE_DIR" ]]; then
    printf '%s\n' "$payload" | ra_artifact_file_write "$RA_PRIVATE_DIR/last-status.json" 2>/dev/null || true
  fi
  printf '%s' "$payload"
}

ra_status_schema_validate() {
  local payload="$1"
  jq -e '
    type=="object" and
    (.connection|type)=="string" and
    ((.mode? // null)==null or (.mode|type)=="string") and
    ((.active_transaction_id? // null)==null or (.active_transaction_id|type)=="string") and
    ((.terminal_reason? // null)==null or (.terminal_reason|type)=="string") and
    ((.lifecycle_phase? // null)==null or (.lifecycle_phase|type)=="string") and
    ((.transactions? // null)==null or
      ((.transactions|type)=="array" and all(.transactions[]?;
        type=="object" and
        ((.id? // null)==null or (.id|type)=="string") and
        ((.state? // null)==null or (.state|type)=="string") and
        ((.requires_cleanup? // null)==null or (.requires_cleanup|type)=="boolean")))) and
    ((.tun_health? // null)==null or
      ((.tun_health|type)=="object" and
       ((.tun_health.state? // null)==null or (.tun_health.state|type)=="string") and
       ((.tun_health.network_generation? // null)==null or (.tun_health.network_generation|type)=="number"))) and
    ((.startup_scan? // null)==null or
      ((.startup_scan|type)=="object" and
       ((.startup_scan.network_session? // null)==null or
        ((.startup_scan.network_session|type)=="object" and
         (.startup_scan.network_session.authority|type)=="string" and
         (.startup_scan.network_session.intent|type)=="string" and
         (.startup_scan.network_session.startup_gate|type)=="string" and
         (.startup_scan.network_session.last_resume_outcome|type)=="string" and
         (.startup_scan.network_session.transaction_present|type)=="boolean" and
         (.startup_scan.network_session.legacy_migration|type)=="boolean" and
         (.startup_scan.network_session.cleanup_authority|type)=="string" and
         (.startup_scan.network_session.next_action|type)=="string" and
         ((.startup_scan.network_session.resume_stage? // null)==null or (.startup_scan.network_session.resume_stage|type)=="string") and
         ((.startup_scan.network_session.last_tun_failure_phase? // null)==null or (.startup_scan.network_session.last_tun_failure_phase|type)=="string") and
         ((.startup_scan.network_session.rollback_status? // null)==null or (.startup_scan.network_session.rollback_status|type)=="string")))))
  ' <<<"$payload" >/dev/null 2>&1
}

ra_status_classify() {
  local target="$1" payload="$2"
  if ! ra_status_schema_validate "$payload"; then
    printf 'CONTRACT_INCOMPATIBLE'
    return 0
  fi
  jq -r --arg target "$target" '
    . as $s |
    ($s.transactions // []) as $txs |
    ($s.startup_scan.network_session // null) as $ns |
    ([ $txs[]? | select((.requires_cleanup//false)==true) ] | length) as $cleanup |
    ([ $txs[]? | select(.state=="committed" and (.requires_cleanup//false)==false) ] | length) as $committed_count |
    (($s.active_transaction_id // "") | tostring) as $active_id |
    (if $active_id!="" then any($txs[]?; (.id//"")==$active_id and .state=="committed" and (.requires_cleanup//false)==false) else $committed_count>0 end) as $committed |
    (($s.tun_health.state // "") | tostring) as $health |
    (($s.terminal_reason // "") | tostring) as $terminal |
    (($s.lifecycle_phase // "") | tostring) as $phase |
    (($ns != null) and ($ns.authority=="present") and
      ($ns.startup_gate=="blocked" or
       $ns.last_resume_outcome=="failed" or
       $ns.last_resume_outcome=="incomplete" or
       $ns.cleanup_authority!="none" or
       $ns.next_action=="manual-diagnosis")) as $session_blocker |
    if $session_blocker or $cleanup>0 or $health=="cleanup-required" then "OWNERSHIP_AMBIGUOUS"
    elif $target=="active" or $target=="active-legacy" then
      if $s.connection=="active" and ($s.mode//"")=="tun" and $committed and
         (($target=="active-legacy" and ($health=="" or $health=="verified")) or ($target=="active" and $health=="verified")) then "TARGET_REACHED"
      elif $phase=="connecting" or $phase=="reconnecting" or $phase=="recovering" or
           ($health=="revalidating" and $s.connection=="active" and ($s.mode//"")=="tun" and $committed) or
           ($s.connection=="error (core exited)" and $health=="revalidating") or
           ($s.connection=="active" and ($s.mode//"")=="tun" and $committed and $health=="") then "TRANSIENT_PROGRESS"
      elif $terminal!="" or $health=="degraded" or $s.connection=="inactive" or ($s.connection|startswith("error (")) then "TERMINAL_PRODUCT_FAILURE"
      else "TERMINAL_PRODUCT_FAILURE" end
    elif $target=="inactive" then
      if $s.connection=="inactive" and $active_id=="" and $committed_count==0 then "TARGET_REACHED"
      elif $phase=="connecting" or $phase=="reconnecting" or $phase=="recovering" or
           $s.connection=="active" or $s.connection=="error (core exited)" then "TRANSIENT_PROGRESS"
      else "TERMINAL_PRODUCT_FAILURE" end
    else "CONTRACT_INCOMPATIBLE" end
  ' <<<"$payload" 2>/dev/null || printf 'CONTRACT_INCOMPATIBLE\n'
}

ra_status_progress_fingerprint() {
  local payload="$1"
  jq -cS '{
    connection:(.connection//""),
    mode:(.mode//""),
    lifecycle_phase:(.lifecycle_phase//""),
    terminal_reason:(.terminal_reason//""),
    tun_health:{state:(.tun_health.state//""),network_generation:(.tun_health.network_generation//null)},
    transactions:[(.transactions//[])[]?|{state:(.state//""),requires_cleanup:(.requires_cleanup//false)}] | sort_by(.state,.requires_cleanup),
    network_session:(if .startup_scan.network_session? then {
      authority:.startup_scan.network_session.authority,
      intent:.startup_scan.network_session.intent,
      startup_gate:.startup_scan.network_session.startup_gate,
      resume_stage:(.startup_scan.network_session.resume_stage//""),
      last_resume_outcome:.startup_scan.network_session.last_resume_outcome,
      last_tun_failure_phase:(.startup_scan.network_session.last_tun_failure_phase//""),
      rollback_status:(.startup_scan.network_session.rollback_status//""),
      transaction_present:.startup_scan.network_session.transaction_present,
      cleanup_authority:.startup_scan.network_session.cleanup_authority,
      next_action:.startup_scan.network_session.next_action
    } else null end)
  }' <<<"$payload" 2>/dev/null
}

ra_status_progress_record() {
  local fingerprint="$1"
  [[ -n "$RA_PRIVATE_DIR" && -d "$RA_PRIVATE_DIR" ]] || return 0
  printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$fingerprint" | ra_artifact_file_append "$RA_PRIVATE_DIR/status-progress.log" 2>/dev/null || true
}

ra_service_wait_classify() {
  local active sub result
  if ! ra_capture systemctl show "$RA_SERVICE" -p ActiveState -p SubState -p Result; then
    printf 'OBSERVATION_UNAVAILABLE'
    return 0
  fi
  active="$(awk -F= '$1=="ActiveState"{print $2;exit}' <<<"$RA_CAPTURE")"
  sub="$(awk -F= '$1=="SubState"{print $2;exit}' <<<"$RA_CAPTURE")"
  result="$(awk -F= '$1=="Result"{print $2;exit}' <<<"$RA_CAPTURE")"
  if [[ -z "$active" || -z "$sub" || -z "$result" ]]; then
    printf 'OBSERVATION_UNAVAILABLE'
  elif [[ "$result" == timeout || "$active" == failed || "$sub" == failed ]]; then
    printf 'SERVICE_FAILURE'
  else
    printf 'SERVICE_OK'
  fi
}

ra_wait_status() {
  local target="$1" timeout="$2" deadline payload classification='' rc fingerprint last_fingerprint='' service
  deadline=$((SECONDS+timeout))
  while ((SECONDS<deadline)); do
    if payload="$(ra_status_json 2>/dev/null)"; then
      classification="$(ra_status_classify "$target" "$payload")" || classification=CONTRACT_INCOMPATIBLE
      case "$classification" in
        TARGET_REACHED) return 0 ;;
        TRANSIENT_PROGRESS)
          fingerprint="$(ra_status_progress_fingerprint "$payload")" || { ra_die "status_contract_incompatible progress fingerprint"; return 1; }
          if [[ "$fingerprint" != "$last_fingerprint" ]]; then
            ra_status_progress_record "$fingerprint"
            last_fingerprint="$fingerprint"
          fi
          ;;
        OWNERSHIP_AMBIGUOUS) ra_die "ownership status lifecycle authority unresolved while waiting for $target"; return 1 ;;
        TERMINAL_PRODUCT_FAILURE) ra_die "product status reached terminal lifecycle state while waiting for $target"; return 1 ;;
        CONTRACT_INCOMPATIBLE) ra_die "status_contract_incompatible while waiting for $target"; return 1 ;;
        *) ra_die "internal unknown status classification: $classification"; return 1 ;;
      esac
    else
      rc=$?
      if ((rc==2)); then
        ra_die "status_contract_incompatible while waiting for $target"
        return 1
      fi
      service="$(ra_service_wait_classify)"
      case "$service" in
        SERVICE_FAILURE) ra_die "product service failure while waiting for $target"; return 1 ;;
        SERVICE_OK|OBSERVATION_UNAVAILABLE) classification=OBSERVATION_UNAVAILABLE ;;
        *) ra_die "internal unknown service classification: $service"; return 1 ;;
      esac
    fi
    sleep 1
  done
  if [[ "$classification" == OBSERVATION_UNAVAILABLE ]]; then
    ra_die "host_state status observation unavailable while waiting for $target"
  else
    ra_die "product status timeout_no_progress while waiting for $target"
  fi
  return 1
}

ra_wait_active() { ra_wait_status active "${1:-120}"; }
ra_wait_active_legacy() { ra_wait_status active-legacy "${1:-120}"; }
ra_wait_inactive() { ra_wait_status inactive "${1:-90}"; }
ra_main_pid() { ra_capture systemctl show -p MainPID --value "$RA_SERVICE" || return 1; [[ "$RA_CAPTURE" =~ ^[0-9]+$ && "$RA_CAPTURE" -gt 1 ]] || return 1; printf '%s' "$RA_CAPTURE"; }
ra_wait_new_pid() { local old="$1" timeout="${2:-60}" deadline current; deadline=$((SECONDS+timeout)); while ((SECONDS<deadline)); do current="$(ra_main_pid 2>/dev/null || true)"; if [[ -n "$current" && "$current" != "$old" ]]; then printf '%s' "$current"; return 0; fi; sleep 1; done; return 1; }
ra_verify_ordinary_network() { ra_capture ip -4 route show table main default || return 1; [[ -n "$RA_CAPTURE" ]] || return 1; ra_capture getent ahostsv4 example.com || return 1; [[ -n "$RA_CAPTURE" ]] || return 1; ra_capture timeout 5 bash -c 'exec 3<>/dev/tcp/example.com/443; exec 3>&-' || return 1; ra_capture curl -4 -fsS --connect-timeout 5 --max-time 10 "$RA_PROBE_URL" -o /dev/null || return 1; }
ra_preflight_clean_boundary() { local installed="$1"; if [[ -z "$installed" ]]; then ra_verify_ordinary_network || { ra_preflight_die "ordinary network is not usable before mutation"; return 2; }; return 0; fi; ra_wait_inactive 5 || { ra_preflight_die "Podlaz must be conclusively disconnected before release acceptance"; return 2; }; ra_verify_ordinary_network || { ra_preflight_die "ordinary network is not usable before mutation"; return 2; }; }

ra_boot_manifest_capture() { if [[ ! -e "$RA_BOOT_MANIFEST" ]]; then jq -cn '{enabled:false}'; return 0; fi; [[ -f "$RA_BOOT_MANIFEST" && ! -L "$RA_BOOT_MANIFEST" ]] || return 1; local size mode uid gid sha payload; size="$(stat -Lc '%s' "$RA_BOOT_MANIFEST")" || return 1; ((size<=65536)) || return 1; mode="$(stat -Lc '%a' "$RA_BOOT_MANIFEST")" || return 1; uid="$(stat -Lc '%u' "$RA_BOOT_MANIFEST")" || return 1; gid="$(stat -Lc '%g' "$RA_BOOT_MANIFEST")" || return 1; sha="$(ra_pkg_sha "$RA_BOOT_MANIFEST")" || return 1; payload="$(base64 -w0 "$RA_BOOT_MANIFEST")" || return 1; jq -cn --arg mode "$mode" --argjson uid "$uid" --argjson gid "$gid" --arg sha "$sha" --arg payload "$payload" '{enabled:true,mode:$mode,uid:$uid,gid:$gid,sha256:$sha,payload_b64:$payload}'; }
ra_manifest_matches_snapshot() { local snap="$1" expected; if [[ "$(jq -r '.enabled' <<<"$snap")" != true ]]; then [[ ! -e "$RA_BOOT_MANIFEST" && ! -L "$RA_BOOT_MANIFEST" ]]; return; fi; [[ -f "$RA_BOOT_MANIFEST" && ! -L "$RA_BOOT_MANIFEST" ]] || return 1; expected="$(jq -r '.sha256' <<<"$snap")" || return 1; [[ "$(ra_pkg_sha "$RA_BOOT_MANIFEST")" == "$expected" ]]; }
ra_boot_manifest_semantic_matches() { local action="$1" profile="${2:-}"; case "$action" in disable) [[ ! -e "$RA_BOOT_MANIFEST" && ! -L "$RA_BOOT_MANIFEST" ]] ;; enable) [[ -f "$RA_BOOT_MANIFEST" && ! -L "$RA_BOOT_MANIFEST" ]] || return 1; jq -e --arg profile "$profile" '.schema_version=="podlaz.boot-autostart-manifest.v1" and .configuration.mode=="tun" and .configuration.profile.id==$profile' "$RA_BOOT_MANIFEST" >/dev/null 2>&1 ;; *) return 1 ;; esac; }
ra_boot_manifest_restore() { local snap="$1"; if [[ "$(jq -r '.enabled' <<<"$snap")" != true ]]; then if [[ -e "$RA_BOOT_MANIFEST" || -L "$RA_BOOT_MANIFEST" ]]; then [[ -f "$RA_BOOT_MANIFEST" && ! -L "$RA_BOOT_MANIFEST" ]] || return 1; rm -f "$RA_BOOT_MANIFEST" || return 1; sync -f "$(dirname "$RA_BOOT_MANIFEST")" 2>/dev/null || true; fi; return 0; fi; local dir tmp mode uid gid expected; dir="$(dirname "$RA_BOOT_MANIFEST")"; mkdir -p "$dir" || return 1; tmp="$(mktemp "$dir/.boot-autostart-manifest.XXXXXX")" || return 1; jq -r '.payload_b64' <<<"$snap" | base64 -d >"$tmp" || { rm -f "$tmp"; return 1; }; expected="$(jq -r '.sha256' <<<"$snap")" || { rm -f "$tmp"; return 1; }; [[ "$(ra_pkg_sha "$tmp")" == "$expected" ]] || { rm -f "$tmp"; return 1; }; mode="$(jq -r '.mode' <<<"$snap")" || return 1; uid="$(jq -r '.uid' <<<"$snap")" || return 1; gid="$(jq -r '.gid' <<<"$snap")" || return 1; chmod "$mode" "$tmp" || return 1; chown "$uid:$gid" "$tmp" || return 1; sync -f "$tmp" 2>/dev/null || true; mv -f "$tmp" "$RA_BOOT_MANIFEST" || return 1; sync -f "$dir" 2>/dev/null || true; ra_manifest_matches_snapshot "$snap"; }

ra_privacy_baseline() { ra_capture ip -4 route show table main default || return 1; local uplink host=example.com port=443 ip; uplink="$(awk '{for(i=1;i<=NF;i++)if($i=="dev"){print $(i+1);exit}}' <<<"$RA_CAPTURE")"; [[ -n "$uplink" && "$uplink" != podlaz0 ]] || return 1; ra_capture getent ahostsv4 "$host" || return 1; ip="$(awk 'NR==1{print $1}' <<<"$RA_CAPTURE")"; [[ "$ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || return 1; ra_capture curl -4 -fsS --interface "$uplink" --connect-timeout 3 --max-time 5 --resolve "$host:$port:$ip" "$RA_PROBE_URL" -o /dev/null || return 1; jq -cn --arg uplink "$uplink" --arg host "$host" --argjson port "$port" --arg ip "$ip" '{uplink:$uplink,host:$host,port:$port,ip:$ip}'; }
ra_privacy_direct_probe() { local b="$1" uplink host port ip; uplink="$(jq -r '.uplink' <<<"$b")" || return 1; host="$(jq -r '.host' <<<"$b")" || return 1; port="$(jq -r '.port' <<<"$b")" || return 1; ip="$(jq -r '.ip' <<<"$b")" || return 1; if ra_capture curl -4 -fsS --interface "$uplink" --connect-timeout 2 --max-time 3 --resolve "$host:$port:$ip" "$RA_PROBE_URL" -o /dev/null; then printf '0'; else printf '%s' "$RA_CAPTURE_RC"; fi; }
ra_privacy_verify_rule() { local rule="$1" kind="$2" protection="$3" tun; case "$kind" in loopback) jq -e 'any(.expr[]?; .accept?!=null) and (.expr|tostring|contains("oifname")) and (.expr|tostring|contains("lo"))' <<<"$rule" >/dev/null ;; tun-egress) tun="$(jq -r '.tun_interface' <<<"$protection")"; jq -e --arg tun "$tun" 'any(.expr[]?; .accept?!=null) and (.expr|tostring|contains("oifname")) and (.expr|tostring|contains($tun))' <<<"$rule" >/dev/null ;; dhcp4) jq -e 'any(.expr[]?; .accept?!=null) and (.expr|tostring|contains("ipv4")) and (.expr|tostring|contains("udp")) and (.expr|tostring|contains("68")) and (.expr|tostring|contains("67"))' <<<"$rule" >/dev/null ;; dhcp6) jq -e 'any(.expr[]?; .accept?!=null) and (.expr|tostring|contains("ipv6")) and (.expr|tostring|contains("udp")) and (.expr|tostring|contains("546")) and (.expr|tostring|contains("547"))' <<<"$rule" >/dev/null ;; ipv6-link-control) jq -e 'any(.expr[]?; .accept?!=null) and (.expr|tostring|contains("icmpv6"))' <<<"$rule" >/dev/null ;; block-direct) jq -e 'any(.expr[]?; .reject?!=null)' <<<"$rule" >/dev/null ;; bootstrap) jq -e --argjson p "$protection" 'any(.expr[]?; .accept?!=null) and ([.expr[]?.match? | select(type=="object") | .right | tostring] | any(. as $v | $p.bootstrap_ipv4 | index($v)!=null))' <<<"$rule" >/dev/null ;; *) return 1 ;; esac; }
ra_privacy_local_proof() { [[ -f "$RA_CONTINUATION" && ! -L "$RA_CONTINUATION" ]] || return 1; local protection table nftjson rules owner rule count expected bootstrap_seen=0; protection="$(jq -ce 'select(.schema_version=="podlaz.network-session-state.v1" and .owner=="podlaz" and (.intent=="resume" or .intent=="terminal"))|.protection|select(type=="object" and (.state=="armed" or .state=="arming" or .state=="removing") and .composition_version==1 and .family=="inet" and (.bootstrap_ipv4|type)=="array" and (.bootstrap_ipv4|length)>0 and (.tun_interface|type)=="string")' "$RA_CONTINUATION" 2>/dev/null)" || return 1; table="$(jq -r '.table' <<<"$protection")" || return 1; [[ "$table" =~ ^podlaz_pe_[0-9a-f]{12}(_[1-9][0-9]{0,2})?$ ]] || return 1; ra_capture nft -j list table inet "$table" || return 1; nftjson="$RA_CAPTURE"; jq -e --arg t "$table" '([.nftables[]?.table?|select(.family=="inet" and .name==$t)]|length)==1 and ([.nftables[]?.chain?|select(.family=="inet" and .table==$t and .name=="output" and .type=="filter" and .hook=="output" and (.prio|tonumber)==-10)]|length)==1' <<<"$nftjson" >/dev/null || return 1; rules="$(jq -c --arg t "$table" '[.nftables[]?.rule?|select(.family=="inet" and .table==$t and .chain=="output")]' <<<"$nftjson")" || return 1; expected=$(( $(jq '.bootstrap_ipv4|length' <<<"$protection") + 6 )); [[ "$(jq length <<<"$rules")" == "$expected" ]] || return 1; for owner in loopback tun-egress dhcp4 dhcp6 ipv6-link-control block-direct; do count="$(jq --arg c "podlaz:privacy-envelope:$owner" '[.[]|select(.comment==$c)]|length' <<<"$rules")" || return 1; [[ "$count" == 1 ]] || return 1; rule="$(jq -c --arg c "podlaz:privacy-envelope:$owner" '.[]|select(.comment==$c)' <<<"$rules")" || return 1; ra_privacy_verify_rule "$rule" "$owner" "$protection" || return 1; done; while IFS= read -r rule; do [[ -n "$rule" ]] || continue; ra_privacy_verify_rule "$rule" bootstrap "$protection" || return 1; ((bootstrap_seen+=1)); done < <(jq -c '.[]|select(.comment=="podlaz:privacy-envelope:bootstrap")' <<<"$rules"); [[ "$bootstrap_seen" == "$(jq '.bootstrap_ipv4|length' <<<"$protection")" ]]; }
ra_privacy_require_protected() { local baseline rc; baseline="$(jq -ce '.private.privacy_baseline' "$RA_CHECKPOINT")" || return 1; rc="$(ra_privacy_direct_probe "$baseline")" || return 1; [[ "$rc" != 0 ]] || { ra_die "product direct_egress_leak"; return 1; }; ra_privacy_local_proof || { ra_die "product inconclusive_local_privacy_authority"; return 1; }; }
ra_privacy_require_ordinary() { local baseline rc; baseline="$(jq -ce '.private.privacy_baseline' "$RA_CHECKPOINT")" || return 1; rc="$(ra_privacy_direct_probe "$baseline")" || return 1; [[ "$rc" == 0 ]] || { ra_die "host_state ordinary_direct_egress_not_restored"; return 1; }; ra_verify_ordinary_network; }
ra_privacy_watch_start() { local label="$1"; RA_PRIVACY_WATCH_STOP="$RA_PRIVATE_DIR/privacy-$label.stop"; RA_PRIVACY_WATCH_FAIL="$RA_PRIVATE_DIR/privacy-$label.fail"; rm -f "$RA_PRIVACY_WATCH_STOP" "$RA_PRIVACY_WATCH_FAIL"; ( while [[ ! -e "$RA_PRIVACY_WATCH_STOP" ]]; do if ! ra_privacy_require_protected >/dev/null 2>&1; then printf 'privacy proof failed during %s\n' "$label" >"$RA_PRIVACY_WATCH_FAIL"; chmod 0600 "$RA_PRIVACY_WATCH_FAIL"; exit 1; fi; sleep 1; done ) & RA_PRIVACY_WATCH_PID=$!; }
ra_privacy_watch_cancel() { if [[ -n "$RA_PRIVACY_WATCH_PID" ]]; then : >"$RA_PRIVACY_WATCH_STOP" 2>/dev/null || true; wait "$RA_PRIVACY_WATCH_PID" 2>/dev/null || true; fi; RA_PRIVACY_WATCH_PID=""; rm -f "$RA_PRIVACY_WATCH_STOP" 2>/dev/null || true; if [[ -n "$RA_BACKGROUND_PID" ]]; then kill "$RA_BACKGROUND_PID" 2>/dev/null || true; wait "$RA_BACKGROUND_PID" 2>/dev/null || true; RA_BACKGROUND_PID=""; fi; }
ra_privacy_watch_stop() { [[ -n "$RA_PRIVACY_WATCH_PID" ]] || return 0; : >"$RA_PRIVACY_WATCH_STOP" || return 1; wait "$RA_PRIVACY_WATCH_PID" || true; RA_PRIVACY_WATCH_PID=""; rm -f "$RA_PRIVACY_WATCH_STOP" 2>/dev/null || true; [[ ! -s "$RA_PRIVACY_WATCH_FAIL" ]] || { ra_die "product privacy protection failed during recovery window"; return 1; }; rm -f "$RA_PRIVACY_WATCH_FAIL" 2>/dev/null || true; ra_privacy_require_protected; }
