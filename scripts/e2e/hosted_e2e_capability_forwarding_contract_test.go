package e2e_test

import "testing"

func TestHostedE2ECapabilityOwnsScopedOuterForwarding(t *testing.T) {
	script := readHostedCapabilityFile(t, hostedCapabilityScript)
	requireHostedCapabilityMarkers(t, script,
		"DOCKER-USER",
		"podlaz-hosted-e2e-forward-out",
		"podlaz-hosted-e2e-forward-in",
		"-m conntrack --ctstate ESTABLISHED,RELATED",
		"iptables -I",
		"iptables -D",
	)
}

func TestHostedE2ECapabilityObservesForwardDropWithoutRejectingRunner(t *testing.T) {
	workflow := readHostedCapabilityFile(t, hostedCapabilityWorkflow)
	requireHostedCapabilityMarkers(t, workflow,
		"Inspect outer forward policy",
		"outer.forward.iptables_policy=drop",
	)
	forbidHostedCapabilityMarkers(t, workflow, "exit 42")
}

func TestHostedE2ECapabilityRetriesTransientOuterControlPlaneFailure(t *testing.T) {
	script := readHostedCapabilityFile(t, hostedCapabilityScript)
	requireHostedCapabilityMarkers(t, script,
		"--retry 3",
		"--retry-max-time 20",
		"https://github.com/",
	)
}

func TestHostedE2ECapabilityReportsSyntheticTunStageBoundaries(t *testing.T) {
	script := readHostedCapabilityFile(t, hostedCapabilityScript)
	requireHostedCapabilityMarkers(t, script,
		"synthetic.xray_endpoint",
		"tun.authorization",
		"tun.synthetic_uri_loaded",
		"tun.profile_import_usage_error",
		"tun.profile_import_arg_error",
		"tun.profile_import_vless_error",
		"tun.profile_import_profile_validation_error",
		"tun.profile_import_usage_other",
		"tun.profile_import_runtime_error",
		"tun.profile_import_other_error",
		"tun.profile_import_command",
		"tun.profile_import_output",
		"tun.profile_import",
		"tun.profile_validate",
		"tun.connect_requested",
	)
}

func TestHostedE2ECapabilityReportsURITransportDiagnostics(t *testing.T) {
	workflow := readHostedCapabilityFile(t, hostedCapabilityWorkflow)
	requireHostedCapabilityMarkers(t, workflow,
		"tun.synthetic_uri_host_loaded=pass",
		"tun.synthetic_uri_host_loaded=fail",
		"tun.guest_argv_transport=pass",
		"tun.guest_argv_transport=fail",
		"tun.guest_uri_punctuation_transport=pass",
		"tun.guest_uri_punctuation_transport=fail",
		"tun.synthetic_uri_argv_length=pass",
		"tun.synthetic_uri_argv_length=fail",
		"tun.synthetic_uri_argv_integrity=pass",
		"tun.synthetic_uri_argv_integrity=fail",
		"tun.synthetic_uri_bind_exists=pass",
		"tun.synthetic_uri_bind_exists=fail",
		"tun.synthetic_uri_bind_readable=pass",
		"tun.synthetic_uri_bind_readable=fail",
		"tun.synthetic_uri_bind_nonempty=pass",
		"tun.synthetic_uri_bind_nonempty=fail",
		"profile-import-transport.log",
	)
}

func TestHostedE2ECapabilityReportsProfileImportArgFailureSubtype(t *testing.T) {
	workflow := readHostedCapabilityFile(t, hostedCapabilityWorkflow)
	requireHostedCapabilityMarkers(t, workflow,
		"tun.profile_import_arg_requires_uri=observed",
		"tun.profile_import_arg_multiple_uri=observed",
		"tun.profile_import_arg_unsupported=observed",
		"tun.profile_import_arg_json=observed",
	)
}

func TestHostedE2ECapabilityClassifiesConnectFailureWithoutLeakingStderr(t *testing.T) {
	workflow := readHostedCapabilityFile(t, hostedCapabilityWorkflow)
	requireHostedCapabilityMarkers(t, workflow,
		"profile-connect-classification.log",
		"tun.connect.authorization_denied=observed",
		"tun.connect.authorization_unavailable=observed",
		"tun.connect.socket_permission=observed",
		"tun.connect.daemon_error=observed",
		"tun.connect.other_error=observed",
	)
	forbidHostedCapabilityMarkers(t, workflow,
		"cat /tmp/podlaz-capability-tun-private/connect.stderr",
		"tail /tmp/podlaz-capability-tun-private/connect.stderr",
	)
}

func TestHostedE2ECapabilityRecordsConnectExitBoundaryPrivately(t *testing.T) {
	workflow := readHostedCapabilityFile(t, hostedCapabilityWorkflow)
	requireHostedCapabilityMarkers(t, workflow,
		"profile-connect-exit.log",
		"tun.connect.exit_zero=observed",
		"tun.connect.exit_nonzero=observed",
		"tun.connect.stage_profile_id=observed",
		"tun.connect.stage_runuser=observed",
		"tun.profile_validate=pass",
		"tun.connect_requested=pass",
	)
	forbidHostedCapabilityMarkers(t, workflow,
		"cat /tmp/podlaz-capability-tun-private/connect.stdout",
		"cat /tmp/podlaz-capability-tun-private/connect.stderr",
	)
}

func TestHostedE2ECapabilityRestagesCandidateIntoLiveGuestTmp(t *testing.T) {
	workflow := readHostedCapabilityFile(t, hostedCapabilityWorkflow)
	requireHostedCapabilityMarkers(t, workflow,
		"Stage candidate into live guest tmpfs",
		"/workspace/${DEV_DEB}",
		"/tmp/candidate.deb",
		"install -m 0644",
	)
}

func TestHostedE2ECapabilityBindsSyntheticURIIntoLiveMachine(t *testing.T) {
	script := readHostedCapabilityFile(t, hostedCapabilityScript)
	requireHostedCapabilityMarkers(t, script,
		"install -d -m 0700 \"${CAPABILITY_XRAY_ROOT}\"",
		"--bind-ro=\"${CAPABILITY_XRAY_ROOT}:/run/podlaz-capability-xray\"",
		"cat /run/podlaz-capability-xray/client-uri",
	)
	forbidHostedCapabilityMarkers(t, script,
		"local import_code uri uri_b64",
		"base64 -d",
		"_ \"${uri_b64}\"",
		"cat /run/podlaz-capability/synthetic-uri",
	)
}

func TestHostedE2ECapabilityWaitsForBoundSyntheticURIReadiness(t *testing.T) {
	script := readHostedCapabilityFile(t, hostedCapabilityScript)
	requireHostedCapabilityMarkers(t, script,
		"wait_guest_synthetic_uri()",
		"guest_exec test -s /run/podlaz-capability-xray/client-uri",
		"wait_guest_synthetic_uri",
	)
}

func TestHostedE2ECapabilityClassifiesSyntheticURIInsideImportShell(t *testing.T) {
	workflow := readHostedCapabilityFile(t, hostedCapabilityWorkflow)
	requireHostedCapabilityMarkers(t, workflow,
		"tun.synthetic_uri_import_shell_stat=pass",
		"tun.synthetic_uri_import_shell_stat=fail",
		"tun.synthetic_uri_import_shell_read=pass",
		"tun.synthetic_uri_import_shell_read=fail",
		"test -s /run/podlaz-capability-xray/client-uri || exit 91",
		"URI=\"$(cat /run/podlaz-capability-xray/client-uri)\" || exit 92",
		"[[ -n \"${URI}\" ]] || exit 90",
		"import_shell_rc=$?",
	)
}

func TestHostedE2ECapabilityDisablesSystemdArgumentEnvironmentExpansion(t *testing.T) {
	script := readHostedCapabilityFile(t, hostedCapabilityScript)
	requireHostedCapabilityMarkers(t, script,
		"guest_exec()",
		"--machine=\"${CAPABILITY_MACHINE}\"",
		"--expand-environment=no",
		"-- \"$@\"",
	)
}

func TestHostedE2ECapabilityKeepsImportedProfileIDInPrivateTmp(t *testing.T) {
	script := readHostedCapabilityFile(t, hostedCapabilityScript)
	requireHostedCapabilityMarkers(t, script,
		"/tmp/podlaz-capability-tun-private/profile-id",
	)
	forbidHostedCapabilityMarkers(t, script,
		"/run/podlaz-capability/profile-id",
	)
}

func TestHostedE2ECapabilityUsesSocketGroupOnlyForDaemonFacingTunCLI(t *testing.T) {
	script := readHostedCapabilityFile(t, hostedCapabilityScript)
	requireHostedCapabilityMarkers(t, script,
		"runuser -u e2e -g podlaz -- env",
		"/usr/bin/podlaz connect --mode tun",
		"/usr/bin/podlaz doctor --tun",
		"/usr/bin/podlaz disconnect",
		"/usr/bin/podlaz recover --json",
	)
}

func TestHostedE2ECapabilityPreservesBoundedPackageFailureDiagnostics(t *testing.T) {
	workflow := readHostedCapabilityFile(t, hostedCapabilityWorkflow)
	requireHostedCapabilityMarkers(t, workflow,
		"Capture volatile package install diagnostics",
		"/run/podlaz-capability-apt.log",
		"hosted-capability-private/candidate-install.log",
		"Collect bounded package install diagnostics",
		"var/log/apt/term.log",
		"var/log/dpkg.log",
		"dpkg-query",
		"tail -n 80",
	)
	forbidHostedCapabilityMarkers(t, workflow, "system-guest/run/podlaz-capability-apt.log")
}
