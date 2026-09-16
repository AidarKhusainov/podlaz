package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostedE2ECapabilityClassifiesSyntheticServerDNSBoundary(t *testing.T) {
	script, err := os.ReadFile("hosted-e2e-capability.sh")
	if err != nil {
		t.Fatalf("read hosted capability script: %v", err)
	}
	text := string(script)
	for _, want := range []string{
		`"log": {"loglevel": "info"}`,
		"capture_synthetic_xray_dns_evidence()",
		"synthetic-server-dns.log",
		"server.log",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("hosted capability must classify the synthetic Xray server DNS boundary; missing %q", want)
		}
	}
	if strings.Count(text, "capture_synthetic_xray_dns_evidence") < 2 {
		t.Fatal("synthetic Xray DNS classifier must be defined and invoked on the failure path")
	}

	tests := []struct {
		name string
		log  string
		want string
	}{
		{
			name: "udp request observed",
			log:  "accepted udp:1.1.1.1:53",
			want: "tun.synthetic_server.udp53_request=observed\ntun.synthetic_server.tcp53_request=missing\n",
		},
		{
			name: "tcp request observed",
			log:  "accepted tcp:1.1.1.1:53",
			want: "tun.synthetic_server.udp53_request=missing\ntun.synthetic_server.tcp53_request=observed\n",
		},
		{
			name: "both requests observed",
			log:  "accepted udp:1.1.1.1:53\naccepted tcp:1.1.1.1:53",
			want: "tun.synthetic_server.udp53_request=observed\ntun.synthetic_server.tcp53_request=observed\n",
		},
		{
			name: "requests missing",
			log:  "accepted tcp:203.0.113.10:443",
			want: "tun.synthetic_server.udp53_request=missing\ntun.synthetic_server.tcp53_request=missing\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tmp := t.TempDir()
			cmd := exec.Command("bash", "-c", `
set -euo pipefail
mkdir -p "$E2E_TMP_ROOT" "$E2E_ARTIFACT_DIR"
export PODLAZ_E2E_CAPABILITY_SOURCE_ONLY=true
source ./hosted-e2e-capability.sh
mkdir -p "$CAPABILITY_XRAY_ROOT"
printf '%s\n' "$SERVER_LOG_FIXTURE" >"$CAPABILITY_XRAY_ROOT/server.log"
capture_synthetic_xray_dns_evidence
`)
			cmd.Env = append(os.Environ(),
				"E2E_TMP_ROOT="+filepath.Join(tmp, "private"),
				"E2E_ARTIFACT_DIR="+filepath.Join(tmp, "public"),
				"SERVER_LOG_FIXTURE="+test.log,
			)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("run synthetic server DNS classifier: %v\n%s", err, output)
			}
			if string(output) != test.want {
				t.Fatalf("classifier output = %q, want %q", output, test.want)
			}
		})
	}
}
