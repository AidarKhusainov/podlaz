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

func TestHostedE2ECapabilityPreservesBoundedPackageFailureDiagnostics(t *testing.T) {
	script := readHostedCapabilityFile(t, hostedCapabilityScript)
	workflow := readHostedCapabilityFile(t, hostedCapabilityWorkflow)
	requireHostedCapabilityMarkers(t, script,
		"/run/podlaz-capability-apt.log",
		"candidate-install.log",
		"stop_system_guest",
	)
	requireHostedCapabilityMarkers(t, workflow,
		"Collect bounded package install diagnostics",
		"var/log/apt/term.log",
		"var/log/dpkg.log",
		"dpkg-query",
		"hosted-capability-private/candidate-install.log",
		"tail -n 80",
	)
	forbidHostedCapabilityMarkers(t, workflow, "system-guest/run/podlaz-capability-apt.log")
}
