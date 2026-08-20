package snapshot

import "testing"

func TestTunLikeInterfaceClassificationIsProductNeutral(t *testing.T) {
	for _, name := range []string{"tun9", "tap2", "wg0", "ppp0", "ipsec0"} {
		if !isForeignTunLikeName(name) {
			t.Fatalf("generic TUN-like interface class %q was not detected", name)
		}
	}

	for _, name := range []string{"tailscale0", "zt0", "proton0", "nordlynx"} {
		if isForeignTunLikeName(name) {
			t.Fatalf("product-specific interface prefix %q must not be classified by product name", name)
		}
	}
}
