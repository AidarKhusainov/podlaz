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
		"tun.profile_import_command",
		"tun.profile_import_output",
		"tun.profile_import",
		"tun.profile_validate",
		"tun.connect_requested",
		"tun.verified_active",
		"tun.clean_disconnect",
		"tun.recovery_clean",
		"artifact.privacy",
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

func TestHostedE2ECapabilityUsesProductionOrdinaryUserBoundary(t *testing.T) {
	script := readHostedCapabilityFile(t, hostedCapabilityScript)
	requireHostedCapabilityMarkers(t, script,
		"assert_tun_authorization_boundary()",
		"runuser -u e2e -- env",
		"root:podlaz:660",
		"errno.EACCES",
		"connect --mode proxy-only",
		"authorization denied",
		"/usr/bin/podlaz connect --mode tun",
		"/usr/bin/podlaz doctor --tun",
		"/usr/bin/podlaz disconnect",
		"/usr/bin/podlaz recover --json",
	)
	forbidHostedCapabilityMarkers(t, script,
		"runuser -u e2e -g podlaz",
	)
}

func TestHostedE2ECapabilityStagesCandidateThroughReadOnlyGuestBind(t *testing.T) {
	script := readHostedCapabilityFile(t, hostedCapabilityScript)
	requireHostedCapabilityMarkers(t, script,
		"CAPABILITY_GUEST_CANDIDATE=\"/opt/podlaz-candidate.deb\"",
		"--bind-ro=\"${CANDIDATE_DEB}:${CAPABILITY_GUEST_CANDIDATE}\"",
		"guest_exec test -r \"${CAPABILITY_GUEST_CANDIDATE}\"",
	)
}
