package e2e_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestReleaseRequiresExactPackageTunSmokeBeforePublish(t *testing.T) {
	contents, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	workflow := string(contents)
	for _, fragment := range []string{
		"tun-smoke:",
		"name: Exact-package TUN smoke",
		"runs-on: [self-hosted, linux, x64, vpn-e2e, ubuntu-24.04]",
		"environment: vpn-e2e",
		"PODLAZ_E2E_PROFILE_URI: ${{ secrets.PODLAZ_E2E_PROFILE_URI }}",
		"PODLAZ_E2E_EXPECTED_EGRESS_IP: ${{ secrets.PODLAZ_E2E_EXPECTED_EGRESS_IP }}",
		"uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1",
		"uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e",
		"uses: actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c",
		"PODLAZ_E2E_DEB_PATH:",
		"PODLAZ_E2E_EXPECT_COMMIT: ${{ needs.build.outputs.commit_sha }}",
		"PODLAZ_E2E_ENABLE_LIFECYCLE: 'true'",
		"PODLAZ_E2E_ENABLE_TUN: 'true'",
		`test -n "${PODLAZ_E2E_EXPECTED_EGRESS_IP}"`,
		"bash scripts/e2e/real-vpn.sh",
		"bash scripts/e2e/tun-package-cleanup.sh",
		`rm -rf "${RUNNER_TEMP}/podlaz-e2e-artifacts"`,
	} {
		if !strings.Contains(workflow, fragment) {
			t.Fatalf("release workflow missing exact-package TUN smoke contract %q", fragment)
		}
	}

	tunSmokeHostedNeeds := regexp.MustCompile(`(?s)tun-smoke:\s*\n.*?needs:\s*\n(?:(?:\s+- .*\n))*?\s+- installed-runtime\s*\n(?:(?:\s+- .*\n))*?\s+- real-provider(?:\s|$)`)
	if !tunSmokeHostedNeeds.MatchString(workflow) {
		t.Fatal("destructive TUN smoke must wait for successful installed-runtime and real-provider hosted qualification")
	}
	publishDependsOnSmoke := regexp.MustCompile(`(?s)attest-and-publish:.*?needs:\s*\n(?:\s*- .*\n)*?\s*- tun-smoke(?:\s|$)`)
	if !publishDependsOnSmoke.MatchString(workflow) {
		t.Fatal("release publication must depend on successful exact-package TUN smoke")
	}
}
