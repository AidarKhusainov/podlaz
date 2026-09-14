package e2e_test

import "testing"

func TestHostedE2ECapabilityReportsOuterForwardPolicy(t *testing.T) {
	script := readHostedCapabilityFile(t, hostedCapabilityScript)
	requireHostedCapabilityMarkers(t, script,
		"outer.forward.iptables_policy=accept",
		"outer.forward.iptables_policy=drop",
	)
}
