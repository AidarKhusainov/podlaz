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
