package e2e_test

import "testing"

func TestHostedE2ECapabilityDistinguishesGuestInternetProbeFailures(t *testing.T) {
	script := readHostedCapabilityFile(t, hostedCapabilityScript)

	requireHostedCapabilityMarkers(t, script,
		`guest.start.internet.dns=fail`,
		`guest.start.internet.https=fail`,
	)
}
