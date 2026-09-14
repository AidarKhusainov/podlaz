# Narrow compatibility boundary for the only historical checkpoints that this
# harness can prove safe: package-only v3/v4 ledgers. This is intentionally not
# a migration framework. Unknown, malformed, or unrepresentable authority is
# never rewritten or cleaned.
RA_LEGACY_CHECKPOINT_MAX_BYTES=1048576

ra_legacy_checkpoint_expected_owner() {
  if [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" == 1 ]]; then
    printf '%s' "${RELEASE_ACCEPTANCE_TEST_CHECKPOINT_OWNER:-$(id -u):$(id -g)}"
  else
    printf '0:0'
  fi
}

ra_legacy_checkpoint_preparse_validate() {
  local size mode owner expected
  [[ -e "$RA_CHECKPOINT" || -L "$RA_CHECKPOINT" ]] || return 1
  [[ -f "$RA_CHECKPOINT" && ! -L "$RA_CHECKPOINT" ]] || return 1
  size="$(stat -Lc '%s' "$RA_CHECKPOINT" 2>/dev/null)" || return 1
  [[ "$size" =~ ^[0-9]+$ ]] || return 1
  ((size > 0 && size <= RA_LEGACY_CHECKPOINT_MAX_BYTES)) || return 1
  mode="$(stat -Lc '%a' "$RA_CHECKPOINT" 2>/dev/null)" || return 1
  [[ "$mode" == 600 ]] || return 1
  owner="$(stat -Lc '%u:%g' "$RA_CHECKPOINT" 2>/dev/null)" || return 1
  expected="$(ra_legacy_checkpoint_expected_owner)" || return 1
  [[ "$owner" == "$expected" ]]
}

ra_legacy_checkpoint_matrix_validate() {
  local schema="$1"
  jq -e --arg schema "$schema" '
    def identity:
      type=="object" and
      ((keys - ["path","package","version","architecture","sha256","device","inode"])|length)==0 and
      (.path|type)=="string" and (.path|startswith("/")) and
      .package=="podlaz" and
      (.version|type)=="string" and (.version|length)>0 and
      (.architecture|type)=="string" and (.architecture|length)>0 and
      (.sha256|type)=="string" and (.sha256|test("^[0-9a-f]{64}$"));
    def identity_core: {path,package,version,architecture,sha256};
    def package_setup:
      type=="object" and
      ((keys - ["state","kind","scenario","identity"])|length)==0 and
      .kind=="previous_package" and
      (.state=="acquiring" or .state=="acquired" or .state=="releasing" or .state=="released") and
      (.identity|type)=="object" and
      ((.identity|keys) - ["previous","candidate"]|length)==0 and
      (.identity|has("previous") and has("candidate")) and
      (.identity.previous|identity) and (.identity.candidate|identity);
    type=="object" and .schema_version==$schema and
    (.candidate|identity) and
    (.mutations|type)=="object" and
    (((.mutations|keys) - ["package_setup"])|length)==0 and
    ((.mutations.package_setup? == null) or
      ((.mutations.package_setup|package_setup) and
       ((.mutations.package_setup.identity.candidate|identity_core)==(.candidate|identity_core))))
  ' "$RA_CHECKPOINT" >/dev/null 2>&1
}

ra_legacy_checkpoint_classify() {
  local schema state
  if [[ ! -e "$RA_CHECKPOINT" && ! -L "$RA_CHECKPOINT" ]]; then
    printf 'none'
    return 0
  fi
  if ! ra_legacy_checkpoint_preparse_validate; then
    printf 'legacy-ambiguous'
    return 0
  fi
  if ! jq -e . "$RA_CHECKPOINT" >/dev/null 2>&1; then
    printf 'legacy-ambiguous'
    return 0
  fi
  schema="$(jq -er '.schema_version|select(type=="string")' "$RA_CHECKPOINT" 2>/dev/null)" || {
    printf 'legacy-ambiguous'
    return 0
  }
  case "$schema" in
    "$RA_SCHEMA") printf 'current'; return 0 ;;
    podlaz.release-acceptance-checkpoint.v3|podlaz.release-acceptance-checkpoint.v4) ;;
    *) printf 'legacy-unsupported'; return 0 ;;
  esac
  if ! ra_legacy_checkpoint_matrix_validate "$schema"; then
    printf 'legacy-ambiguous'
    return 0
  fi
  state="$(jq -r '.mutations.package_setup.state//"released"' "$RA_CHECKPOINT")" || {
    printf 'legacy-ambiguous'
    return 0
  }
  if [[ "$state" == released ]]; then printf 'legacy-clean'; else printf 'legacy-cleanable'; fi
}

ra_legacy_identity_inspect_exact() {
  local expected="$1" path actual
  path="$(jq -er '.path' <<<"$expected")" || return 1
  actual="$(ra_pkg_inspect "$path")" || return 1
  jq -e --argjson expected "$expected" '
    .path==$expected.path and .package==$expected.package and
    .version==$expected.version and .architecture==$expected.architecture and
    .sha256==$expected.sha256
  ' <<<"$actual" >/dev/null || return 1
  printf '%s' "$actual"
}

ra_legacy_installed_package_identity() {
  local status package version architecture
  if ! ra_capture dpkg-query -W '-f=${Status}\t${Package}\t${Version}\t${Architecture}\n' podlaz; then
    return 1
  fi
  IFS=$'\t' read -r status package version architecture <<<"$RA_CAPTURE"
  [[ "$status" == "install ok installed" && "$package" == podlaz && -n "$version" && -n "$architecture" ]] || return 1
  jq -cn --arg package "$package" --arg version "$version" --arg architecture "$architecture" '{package:$package,version:$version,architecture:$architecture}'
}

ra_legacy_unrepresentable_authority_absent() {
  local path
  for path in "$RA_CONTINUATION" "$RA_BOOT_ATTEMPT" "$RA_ROLLBACK_HOOK_DIR" "$RA_ROLLBACK_OVERRIDE" "$RA_TERMINAL_HOOK_DIR" "$RA_TERMINAL_OVERRIDE"; do
    [[ ! -e "$path" && ! -L "$path" ]] || return 1
  done
  if [[ -e "$RA_TRANSACTIONS" || -L "$RA_TRANSACTIONS" ]]; then
    [[ -d "$RA_TRANSACTIONS" && ! -L "$RA_TRANSACTIONS" ]] || return 1
    [[ -z "$(find "$RA_TRANSACTIONS" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]] || return 1
  fi
}

ra_legacy_clean_boundary_prove() {
  ra_verify_inactive_boundary || return 1
  ra_verify_ordinary_network || return 1
}

ra_legacy_checkpoint_archive() {
  local schema="$1" digest archive_dir target expected_owner owner tmp
  digest="$(ra_pkg_sha "$RA_CHECKPOINT")" || return 1
  archive_dir="$RA_STATE_DIR/legacy-checkpoint-archive"
  expected_owner="$(ra_legacy_checkpoint_expected_owner)" || return 1

  if [[ -e "$archive_dir" || -L "$archive_dir" ]]; then
    [[ -d "$archive_dir" && ! -L "$archive_dir" ]] || return 1
  else
    (umask 077; mkdir -- "$archive_dir") || return 1
  fi
  chmod 0700 -- "$archive_dir" || return 1
  if [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" != 1 ]]; then
    chown root:root -- "$archive_dir" || return 1
  fi
  owner="$(stat -Lc '%u:%g' "$archive_dir")" || return 1
  [[ "$owner" == "$expected_owner" ]] || return 1

  target="$archive_dir/${schema##*.}-$digest.json"
  if [[ -e "$target" || -L "$target" ]]; then
    [[ -f "$target" && ! -L "$target" ]] || return 1
    [[ "$(stat -Lc '%a' "$target")" == 600 ]] || return 1
    owner="$(stat -Lc '%u:%g' "$target")" || return 1
    [[ "$owner" == "$expected_owner" ]] || return 1
    [[ "$(ra_pkg_sha "$target")" == "$digest" ]] || return 1
    return 0
  fi

  tmp="$(mktemp "$archive_dir/.legacy-checkpoint.XXXXXX")" || return 1
  if ! cat "$RA_CHECKPOINT" >"$tmp"; then rm -f -- "$tmp"; return 1; fi
  chmod 0600 -- "$tmp" || { rm -f -- "$tmp"; return 1; }
  if [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" != 1 ]]; then
    chown root:root -- "$tmp" || { rm -f -- "$tmp"; return 1; }
  fi
  owner="$(stat -Lc '%u:%g' "$tmp")" || { rm -f -- "$tmp"; return 1; }
  [[ "$owner" == "$expected_owner" ]] || { rm -f -- "$tmp"; return 1; }
  [[ "$(ra_pkg_sha "$tmp")" == "$digest" ]] || { rm -f -- "$tmp"; return 1; }

  if [[ -e "$target" || -L "$target" ]]; then
    rm -f -- "$tmp"
    [[ -f "$target" && ! -L "$target" ]] || return 1
    [[ "$(ra_pkg_sha "$target")" == "$digest" ]] || return 1
    return 0
  fi
  mv -- "$tmp" "$target" || { rm -f -- "$tmp"; return 1; }
  sync -f "$target" 2>/dev/null || true
  sync -f "$archive_dir" 2>/dev/null || true
}

ra_legacy_checkpoint_reconcile() {
  local classification schema state candidate_expected candidate previous_expected="" previous="" installed installed_version installed_arch candidate_version candidate_arch previous_version=""
  classification="$(ra_legacy_checkpoint_classify)" || return 1
  case "$classification" in
    none) return 0 ;;
    legacy-clean|legacy-cleanable) ;;
    legacy-unsupported) ra_die "legacy checkpoint unsupported; refusing mutation"; return 1 ;;
    *) ra_die "legacy checkpoint ambiguous; refusing mutation"; return 1 ;;
  esac

  schema="$(jq -er '.schema_version' "$RA_CHECKPOINT")" || return 1
  state="$(jq -r '.mutations.package_setup.state//"released"' "$RA_CHECKPOINT")" || return 1
  candidate_expected="$(jq -ce '.candidate' "$RA_CHECKPOINT")" || return 1
  candidate="$(ra_legacy_identity_inspect_exact "$candidate_expected")" || { ra_die "legacy checkpoint ambiguous: candidate package identity mismatch"; return 1; }
  candidate_version="$(jq -r '.version' <<<"$candidate")" || return 1
  candidate_arch="$(jq -r '.architecture' <<<"$candidate")" || return 1

  if jq -e '.mutations.package_setup? != null' "$RA_CHECKPOINT" >/dev/null 2>&1; then
    previous_expected="$(jq -ce '.mutations.package_setup.identity.previous' "$RA_CHECKPOINT")" || return 1
    previous="$(ra_legacy_identity_inspect_exact "$previous_expected")" || { ra_die "legacy checkpoint ambiguous: previous package identity mismatch"; return 1; }
    previous_version="$(jq -r '.version' <<<"$previous")" || return 1
    [[ "$(jq -r '.architecture' <<<"$previous")" == "$candidate_arch" ]] || { ra_die "legacy checkpoint ambiguous: package architecture mismatch"; return 1; }
    dpkg --compare-versions "$previous_version" lt "$candidate_version" || { ra_die "legacy checkpoint ambiguous: previous package is not strictly older"; return 1; }
  fi

  installed="$(ra_legacy_installed_package_identity)" || { ra_die "legacy checkpoint ambiguous: current installed package evidence unavailable"; return 1; }
  [[ "$(jq -r '.package' <<<"$installed")" == podlaz ]] || { ra_die "legacy checkpoint ambiguous: installed package name mismatch"; return 1; }
  installed_version="$(jq -r '.version' <<<"$installed")" || return 1
  installed_arch="$(jq -r '.architecture' <<<"$installed")" || return 1
  [[ "$installed_arch" == "$candidate_arch" ]] || { ra_die "legacy checkpoint ambiguous: installed package architecture mismatch"; return 1; }

  ra_legacy_unrepresentable_authority_absent || { ra_die "legacy checkpoint ambiguous: retained Network Session/transaction authority"; return 1; }
  ra_legacy_clean_boundary_prove || { ra_die "legacy checkpoint ambiguous: inactive ordinary-network boundary not proven"; return 1; }

  if [[ "$installed_version" == "$candidate_version" ]]; then
    :
  elif [[ "$state" != released && -n "$previous_version" && "$installed_version" == "$previous_version" ]]; then
    ra_pkg_install_exact "$candidate" || { ra_die "legacy checkpoint candidate restoration failed"; return 1; }
    installed="$(ra_legacy_installed_package_identity)" || return 1
    jq -e --arg v "$candidate_version" --arg a "$candidate_arch" '.package=="podlaz" and .version==$v and .architecture==$a' <<<"$installed" >/dev/null || { ra_die "legacy checkpoint candidate restoration evidence mismatch"; return 1; }
    ra_legacy_unrepresentable_authority_absent || { ra_die "legacy checkpoint ambiguous after package restoration"; return 1; }
    ra_legacy_clean_boundary_prove || { ra_die "legacy checkpoint clean boundary not restored"; return 1; }
  else
    ra_die "legacy checkpoint ambiguous: installed package is neither exact candidate nor provable previous package"
    return 1
  fi

  ra_legacy_checkpoint_archive "$schema" || { ra_die "legacy checkpoint archive failed; checkpoint retained"; return 1; }
  ra_state_remove || { ra_die "legacy checkpoint retirement failed after archive"; return 1; }
}

# Controller wrappers add only the legacy checkpoint compatibility gate.
# Current-v5 behavior delegates to the integrated controller above so later
# artifact/evidence/status fixes are not duplicated or shadowed here.
ra_run_new() {
  local kind
  kind="$(ra_legacy_checkpoint_classify)"
  case "$kind" in
    none|current) ra_run_new_current ;;
    legacy-clean|legacy-cleanable) ra_legacy_checkpoint_reconcile || { RA_SUPPRESS_FINALIZER=1; return 1; }; ra_run_new_fresh ;;
    legacy-unsupported) RA_SUPPRESS_FINALIZER=1; ra_die "legacy checkpoint unsupported; refusing mutation"; return 1 ;;
    legacy-ambiguous) RA_SUPPRESS_FINALIZER=1; ra_die "legacy checkpoint ambiguous; refusing mutation"; return 1 ;;
    *) RA_SUPPRESS_FINALIZER=1; return 1 ;;
  esac
}

ra_run_resume() {
  local kind
  kind="$(ra_legacy_checkpoint_classify)"
  case "$kind" in
    current) ra_run_resume_current ;;
    none) return 2 ;;
    legacy-unsupported) RA_SUPPRESS_FINALIZER=1; ra_die "legacy checkpoint unsupported for resume; use --abort or --restart only when it is safely cleanable"; return 1 ;;
    legacy-clean|legacy-cleanable|legacy-ambiguous) RA_SUPPRESS_FINALIZER=1; ra_die "legacy checkpoint ambiguous for resume; refusing mutation"; return 1 ;;
    *) RA_SUPPRESS_FINALIZER=1; return 1 ;;
  esac
}

ra_run_abort() {
  local kind
  kind="$(ra_legacy_checkpoint_classify)"
  case "$kind" in
    current) ra_run_abort_current ;;
    none) return 2 ;;
    legacy-clean|legacy-cleanable) ra_legacy_checkpoint_reconcile || return 1; printf 'ABORTED_CLEAN\n' ;;
    legacy-unsupported) ra_die "legacy checkpoint unsupported; refusing mutation"; return 1 ;;
    legacy-ambiguous) ra_die "legacy checkpoint ambiguous; refusing mutation"; return 1 ;;
    *) return 1 ;;
  esac
}

ra_run_restart() {
  local kind
  kind="$(ra_legacy_checkpoint_classify)"
  case "$kind" in
    none|current) ra_run_restart_current ;;
    legacy-clean|legacy-cleanable) ra_legacy_checkpoint_reconcile || { RA_SUPPRESS_FINALIZER=1; return 1; }; RA_MODE=new; ra_run_new_fresh ;;
    legacy-unsupported) RA_SUPPRESS_FINALIZER=1; ra_die "legacy checkpoint unsupported; refusing mutation"; return 1 ;;
    legacy-ambiguous) RA_SUPPRESS_FINALIZER=1; ra_die "legacy checkpoint ambiguous; refusing mutation"; return 1 ;;
    *) RA_SUPPRESS_FINALIZER=1; return 1 ;;
  esac
}
