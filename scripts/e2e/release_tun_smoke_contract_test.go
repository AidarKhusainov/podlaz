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

	tunStart := strings.Index(workflow, "\n  tun-smoke:\n")
	installedStart := strings.Index(workflow, "\n  installed-runtime:\n")
	if tunStart < 0 || installedStart < 0 || installedStart <= tunStart {
		t.Fatal("cannot isolate tun-smoke job from release workflow")
	}
	tunBlock := workflow[tunStart:installedStart]
	for _, dependency := range []string{"      - installed-runtime\n", "      - real-provider\n"} {
		if !strings.Contains(tunBlock, dependency) {
			t.Fatalf("destructive TUN smoke must wait for hosted qualification dependency %q", strings.TrimSpace(dependency))
		}
	}

	publishDependsOnSmoke := regexp.MustCompile(`(?s)attest-and-publish:.*?needs:\s*\n(?:\s*- .*\n)*?\s*- tun-smoke(?:\s|$)`)
	if !publishDependsOnSmoke.MatchString(workflow) {
		t.Fatal("release publication must depend on successful exact-package TUN smoke")
	}
}
