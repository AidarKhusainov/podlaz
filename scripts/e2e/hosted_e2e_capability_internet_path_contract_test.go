package e2e_test

import "testing"

func TestHostedE2ECapabilityDistinguishesGuestInternetPathFailures(t *testing.T) {
	script := readHostedCapabilityFile(t, hostedCapabilityScript)
	requireHostedCapabilityMarkers(t, script,
		"guest.start.internet.gateway=fail",
		"guest.start.internet.egress=fail",
	)
}
