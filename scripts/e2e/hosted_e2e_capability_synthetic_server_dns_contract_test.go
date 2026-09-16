package e2e

import (
	"os"
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
		"tun.synthetic_server.udp53_request=observed",
		"tun.synthetic_server.udp53_request=missing",
		"tun.synthetic_server.tcp53_request=observed",
		"tun.synthetic_server.tcp53_request=missing",
		"capture_synthetic_xray_dns_evidence",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("hosted capability must classify the synthetic Xray server DNS boundary; missing %q", want)
		}
	}
}
