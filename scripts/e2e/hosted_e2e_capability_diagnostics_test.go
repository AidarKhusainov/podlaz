package e2e_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestHostedE2ECapabilityClassifiesSyntheticVLESSRejectCause(t *testing.T) {
	tests := []struct {
		name     string
		cause    string
		expected string
	}{
		{name: "invalid version", cause: "invalid request version", expected: "tun.synthetic_server.reject_invalid_version=observed"},
		{name: "invalid user id", cause: "invalid request user id: 00000000-0000-4000-8000-000000000001", expected: "tun.synthetic_server.reject_invalid_user_id=observed"},
		{name: "header addons", cause: "failed to decode request header addons", expected: "tun.synthetic_server.reject_header_addons=observed"},
		{name: "request command", cause: "failed to read request command", expected: "tun.synthetic_server.reject_request_command=observed"},
		{name: "invalid address", cause: "invalid request address", expected: "tun.synthetic_server.reject_invalid_address=observed"},
		{name: "other", cause: "unexpected synthetic rejection", expected: "tun.synthetic_server.reject_other=observed"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tmp := t.TempDir()
			command := `
set -euo pipefail
export PODLAZ_E2E_CAPABILITY_SOURCE_ONLY=true
export E2E_TMP_ROOT="$1/private"
export E2E_ARTIFACT_DIR="$1/public"
mkdir -p "$E2E_TMP_ROOT" "$E2E_ARTIFACT_DIR"
source ./hosted-e2e-capability.sh
mkdir -p "$CAPABILITY_XRAY_ROOT"
printf 'proxy/vless/inbound: firstLen = 64\nproxy/vless/inbound: invalid request from 172.31.255.2:42424 > proxy/vless/encoding: %s\n' "$2" >"$CAPABILITY_XRAY_ROOT/server.log"
capture_synthetic_xray_dns_evidence
`
			cmd := exec.Command("bash", "-c", command, "bash", tmp, test.cause)
			cmd.Dir = "."
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("classify synthetic VLESS rejection: %v\n%s", err, output)
			}
			text := string(output)
			if !strings.Contains(text, test.expected) {
				t.Fatalf("synthetic VLESS rejection did not classify %q; output:\n%s", test.expected, text)
			}
			for _, sensitive := range []string{"172.31.255.2", "00000000-0000-4000-8000-000000000001"} {
				if strings.Contains(text, sensitive) {
					t.Fatalf("synthetic VLESS diagnostics leaked raw rejection data %q: %s", sensitive, text)
				}
			}
		})
	}
}

func TestHostedE2ECapabilityWorkflowDisablesGuestSystemdRunEnvironmentExpansion(t *testing.T) {
	data, err := os.ReadFile(hostedCapabilityWorkflow)
	if err != nil {
		t.Fatalf("read hosted capability workflow: %v", err)
	}

	lines := strings.Split(string(data), "\n")
	probes := 0
	for i, line := range lines {
		if !strings.Contains(line, "sudo -n systemd-run \\") {
			continue
		}

		end := i + 9
		if end > len(lines) {
			end = len(lines)
		}
		block := strings.Join(lines[i:end], "\n")
		if !strings.Contains(block, "--machine=podlaz-capability") {
			continue
		}
		probes++
		if !strings.Contains(block, "--expand-environment=no") {
			t.Fatalf("guest systemd-run invocation must disable manager-side environment expansion:\n%s", block)
		}
	}
	if probes == 0 {
		t.Fatal("hosted capability workflow no longer contains guest systemd-run probes")
	}
}
