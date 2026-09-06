package e2e_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestReleaseUsesHostedQualificationWithoutRetiredRunner(t *testing.T) {
	contents, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	workflow := string(contents)

	for _, forbidden := range []string{
		"tun-smoke:",
		"Exact-package TUN smoke",
		"self-hosted",
		"vpn-e2e, ubuntu-24.04",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("release workflow still depends on retired runner infrastructure %q", forbidden)
		}
	}

	publish := regexp.MustCompile(`(?s)attest-and-publish:.*?needs:\s*\n(?P<needs>(?:\s*- [^\n]+\n)+)`).FindStringSubmatch(workflow)
	if publish == nil {
		t.Fatal("release publication needs block is missing")
	}
	needs := publish[1]
	for _, required := range []string{"- resolve", "- build", "- installed-runtime", "- real-provider"} {
		if !strings.Contains(needs, required) {
			t.Fatalf("release publication lost hosted qualification dependency %q", required)
		}
	}
}

func TestLocalVPNValidationScriptsRemainAvailable(t *testing.T) {
	for _, path := range []string{
		"real-vpn.sh",
		"tun-package-cleanup.sh",
		"../acceptance/release-laptop.sh",
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("local VPN validation script %s must remain available: %v", path, err)
		}
		if !info.Mode().IsRegular() {
			t.Fatalf("local VPN validation script %s is not a regular file", path)
		}
		if info.Mode().Perm()&0111 == 0 {
			t.Fatalf("local VPN validation script %s must remain executable", path)
		}
	}
}
