package daemon

import (
	"os"
	"strings"
	"testing"
)

func TestIssue260ProductionSourceHasNoForeignVPNControlAdapter(t *testing.T) {
	files := []string{
		"tun_handoff_preflight.go",
		"../network/snapshot/collect.go",
	}
	forbidden := []string{
		"nmcli connection down",
		"tailscale",
		"proton",
		"nord",
		"mullvad",
		"openvpn",
		"throne",
	}

	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := strings.ToLower(string(data))
		for _, token := range forbidden {
			if strings.Contains(text, token) {
				t.Fatalf("%s contains forbidden product-specific coexistence token %q", path, token)
			}
		}
	}
}
