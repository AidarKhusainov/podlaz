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

func TestHostedE2ECapabilityPassesSyntheticURIAsImportArgument(t *testing.T) {
	script := readHostedCapabilityFile(t, hostedCapabilityScript)
	requireHostedCapabilityMarkers(t, script,
		"local import_code uri",
		"uri=\"$(<\"${CAPABILITY_XRAY_ROOT}/client-uri\")\"",
		"URI=\\\"\\$1\\\"",
		"_ \"${uri}\"",
	)
	forbidHostedCapabilityMarkers(t, script,
		"URI=\\$(cat)",
		"<\"${CAPABILITY_XRAY_ROOT}/client-uri\"",
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
