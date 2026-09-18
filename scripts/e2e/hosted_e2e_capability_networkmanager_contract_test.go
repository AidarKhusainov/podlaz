package e2e_test

import "testing"

func TestHostedE2ECapabilityShadowsUbuntuStrictUnmanagedDefault(t *testing.T) {
	script := readHostedCapabilityFile(t, hostedCapabilityScript)

	requireHostedCapabilityMarkers(t, script,
		`sudo -n install -D -m 0644 /dev/null "${CAPABILITY_GUEST_ROOT}/etc/NetworkManager/conf.d/10-globally-managed-devices.conf"`,
	)
}
