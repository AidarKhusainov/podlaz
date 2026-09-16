package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestHostedE2ECapabilityClassifiesOuterDNSTransport(t *testing.T) {
	workflow, err := os.ReadFile("../../.github/workflows/hosted-e2e-capability.yml")
	if err != nil {
		t.Fatalf("read hosted capability workflow: %v", err)
	}
	text := string(workflow)

	for _, want := range []string{
		"Probe outer DNS transport",
		"outer.dns.udp53_response=observed",
		"outer.dns.udp53_timeout=observed",
		"outer.dns.udp53_error=observed",
		"outer.dns.tcp53_connect=observed",
		"outer.dns.tcp53_timeout=observed",
		"outer.dns.tcp53_error=observed",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("hosted capability workflow must classify outer DNS transport; missing %q", want)
		}
	}
}
