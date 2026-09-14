package e2e_test

import "testing"

func TestHostedE2ECapabilityReportsOuterForwardPolicy(t *testing.T) {
	workflow := readHostedCapabilityFile(t, hostedCapabilityWorkflow)
	requireHostedCapabilityMarkers(t, workflow,
		"outer.forward.iptables_policy=accept",
		"outer.forward.iptables_policy=drop",
	)
}
