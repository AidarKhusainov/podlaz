package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	productstatus "github.com/AidarKhusainov/podlaz/internal/status"
)

func TestTunResourceSoakPreservesLifecycleAndPrivacyBoundaries(t *testing.T) {
	data, err := os.ReadFile("tun-resource-soak.sh")
	if err != nil {
		t.Fatalf("read TUN resource soak harness: %v", err)
	}
	text := string(data)

	ordered := []string{
		"precondition_warmed_inactive_baseline",
		`SOAK_PHASE="session-one-connect"`,
		`--output "${SESSION_ONE_IDENTITY}"`,
		`sleep "${PODLAZ_E2E_SOAK_WARMUP_SECONDS}"`,
		"run_active_soak",
		"disconnect_and_sample_cleanup",
		"run_reconnect_probe",
		"write_public_report",
		`rm -rf -- "${SOAK_PRIVATE_DIR}"`,
		"assert_artifacts_do_not_contain_sensitive_values",
	}
	previous := -1
	for _, marker := range ordered {
		index := strings.LastIndex(text, marker)
		if index < 0 {
			t.Fatalf("TUN resource soak lost lifecycle boundary %q", marker)
		}
		if index <= previous {
			t.Fatalf("TUN resource soak lifecycle boundary %q is out of order", marker)
		}
		previous = index
	}

	for _, required := range []string{
		`assert-replaced`,
		`assert-gone`,
		`NETWORK_ISOLATION_BASELINE`,
		"capture_direct_uplink_privacy_baseline",
		"assert_exact_active_authority",
		"hosted_synthetic_active_authority.py",
		"assert_direct_uplink_blocked",
		"Privacy Envelope allowed ordinary direct uplink egress",
		`PACKAGE_BUILD_LOG="${SOAK_PRIVATE_DIR}/package-build.log"`,
		`PACKAGE_INSTALL_LOG="${SOAK_PRIVATE_DIR}/package-install.log"`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("TUN resource soak lost %q", required)
		}
	}
}

func TestTunSoakStatusClassifierMatchesCurrentProductView(t *testing.T) {
	cases := []struct {
		name     string
		view     productstatus.ProductStatusView
		exitCode int
		want     string
	}{
		{"verified-tun", productstatus.ProductStatusView{State: productstatus.ProductConnected, ProfileName: "Example VPN", Protection: "Active", AutostartKnown: true}, 0, "verified"},
		{"proxy-only", productstatus.ProductStatusView{State: productstatus.ProductConnected, ProfileName: "Example VPN", Protection: "Proxy only"}, 0, "invalid-status"},
		{"connecting", productstatus.ProductStatusView{State: productstatus.ProductConnecting, ProfileName: "Example VPN", Protection: "Active"}, 0, "retry-initializing"},
		{"reconnecting", productstatus.ProductStatusView{State: productstatus.ProductReconnecting, ProfileName: "Example VPN", Protection: "Active"}, 3, "retry-revalidating"},
		{"unknown", productstatus.ProductStatusView{State: productstatus.ProductUnknown, Reason: "Connection state could not be determined"}, 3, "invalid-status"},
		{"disconnected", productstatus.ProductStatusView{State: productstatus.ProductDisconnected, AutostartKnown: true}, 0, "terminal-inactive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout := tc.view.String()
			if strings.Contains(stdout, "Mode: ") {
				t.Fatal("production product view unexpectedly exposes legacy Mode field")
			}
			path := filepath.Join(t.TempDir(), "private-status.stdout")
			if err := os.WriteFile(path, []byte(stdout), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("python3", "lib/tun_soak_status.py", "classify",
				"--stdout-file", path, "--exit-code", strconv.Itoa(tc.exitCode))
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("status classifier failed: %v; output=%q", err, output)
			}
			if got := strings.TrimSpace(string(output)); got != tc.want {
				t.Fatalf("classifier verdict=%q, want %q", got, tc.want)
			}
		})
	}
}
