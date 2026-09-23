package e2e_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestReleaseUsesExactHostedQualificationWithoutRetiredRunner(t *testing.T) {
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
		"release-laptop.sh",
		"scripts/acceptance/",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("release workflow still depends on retired/manual infrastructure %q", forbidden)
		}
	}

	publish := regexp.MustCompile(`(?s)attest-and-publish:.*?needs:\s*\n(?P<needs>(?:\s*- [^\n]+\n)+)`).FindStringSubmatch(workflow)
	if publish == nil {
		t.Fatal("release publication needs block is missing")
	}
	needs := publish[1]
	for _, required := range []string{"- resolve", "- build", "- installed-runtime", "- synthetic-tun", "- real-provider"} {
		if !strings.Contains(needs, required) {
			t.Fatalf("release publication lost required qualification dependency %q", required)
		}
	}

	for _, required := range []string{
		"release_manifest_sha256: ${{ steps.release-manifest.outputs.sha256 }}",
		"name: Qualify exact synthetic full-TUN package",
		"PODLAZ_E2E_CANDIDATE_COMMIT: ${{ needs.build.outputs.commit_sha }}",
		`bash scripts/e2e/hosted-synthetic-tun.sh "dist/release/podlaz_${{ needs.resolve.outputs.version }}_linux_amd64.deb"`,
		"name: Verify exact qualified release artifact handoff",
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("release exact-artifact contract is missing %q", required)
		}
	}
	if got := strings.Count(workflow, "bash scripts/ci/verify-release-artifacts.sh"); got < 4 {
		t.Fatalf("release artifact handoff must be verified by every required qualifier and publisher, got %d checks", got)
	}
}


func TestReleasePublisherChecksOutTagBeforeRepositoryScripts(t *testing.T) {
	contents, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	workflow := string(contents)

	publishIndex := strings.Index(workflow, "  attest-and-publish:\n")
	if publishIndex < 0 {
		t.Fatal("release publisher job is missing")
	}
	publisher := workflow[publishIndex:]
	verifyIndex := strings.Index(publisher, "      - name: Verify exact qualified release artifact handoff\n")
	if verifyIndex < 0 {
		t.Fatal("release publisher exact-artifact verification step is missing")
	}
	beforeVerify := publisher[:verifyIndex]
	for _, required := range []string{
		"      - name: Checkout exact release tag\n",
		"          ref: ${{ needs.resolve.outputs.tag }}\n",
		"          persist-credentials: false\n",
	} {
		if !strings.Contains(beforeVerify, required) {
			t.Fatalf("release publisher must checkout the exact release tag before repository scripts; missing %q", required)
		}
	}
}


func TestReleaseSupportsExactTagRecoveryDispatch(t *testing.T) {
	contents, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	workflow := string(contents)

	for _, required := range []string{
		"  workflow_dispatch:\n",
		"      tag:\n",
		"concurrency:\n  group: release-${{ inputs.tag || github.ref_name }}\n",
		"          REQUESTED_TAG: ${{ inputs.tag || github.ref_name }}\n",
		"          tag=\"${REQUESTED_TAG}\"\n",
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("release recovery dispatch contract is missing %q", required)
		}
	}
}

func TestReleasePublishesHumanReadableUpgradeNotes(t *testing.T) {
	contents, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	workflow := string(contents)

	for _, required := range []string{
		"--generate-notes",
		"Install / upgrade:",
		"https://github.com/${GH_REPO}#install-from-a-github-release",
		"https://github.com/${GH_REPO}#upgrade-rollback-and-uninstall",
		"SHA256SUMS",
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("release workflow is missing human-readable release-note contract %q", required)
		}
	}
	if got := strings.Count(workflow, `--notes "${notes}"`); got != 1 {
		t.Fatalf("release notes must be written only on creation so reruns preserve generated notes, got %d writers", got)
	}
}

func TestReleaseArtifactVerifierRejectsMutation(t *testing.T) {
	version := "1.2.3"
	dir := t.TempDir()
	assets := []string{
		fmt.Sprintf("podlaz_%s_linux_amd64.tar.gz", version),
		fmt.Sprintf("podlaz_%s_linux_arm64.tar.gz", version),
		fmt.Sprintf("podlaz_%s_linux_amd64.deb", version),
		fmt.Sprintf("podlaz_%s_linux_arm64.deb", version),
	}
	for i, name := range assets {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(fmt.Sprintf("asset-%d\n", i)), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	cmd := exec.Command("sha256sum", assets...)
	cmd.Dir = dir
	manifest, err := cmd.Output()
	if err != nil {
		t.Fatalf("create checksum manifest: %v", err)
	}
	manifestPath := filepath.Join(dir, "SHA256SUMS")
	if err := os.WriteFile(manifestPath, manifest, 0o600); err != nil {
		t.Fatalf("write checksum manifest: %v", err)
	}
	manifestDigest := fmt.Sprintf("%x", sha256.Sum256(manifest))

	run := func() error {
		cmd := exec.Command("bash", "../../scripts/ci/verify-release-artifacts.sh", dir)
		cmd.Env = append(os.Environ(),
			"VERSION="+version,
			"EXPECTED_RELEASE_MANIFEST_SHA256="+manifestDigest,
		)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%w: %s", err, output)
		}
		return nil
	}
	if err := run(); err != nil {
		t.Fatalf("valid release artifacts rejected: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, assets[0]), []byte("mutated\n"), 0o600); err != nil {
		t.Fatalf("mutate asset: %v", err)
	}
	if err := run(); err == nil {
		t.Fatal("mutated release artifact unexpectedly passed exact-artifact verification")
	}
}

func TestReleaseLaptopIsOptionalDiagnosticOnly(t *testing.T) {
	path := "../acceptance/release-laptop.sh"
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("optional release-laptop diagnostic must remain available: %v", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("optional release-laptop diagnostic must remain an executable regular file")
	}

	contents, err := os.ReadFile("../acceptance/lib/release-laptop/core.sh")
	if err != nil {
		t.Fatalf("read release-laptop help: %v", err)
	}
	if !strings.Contains(string(contents), "Optional developer diagnostic only; it is not a release gate or required release acceptance.") {
		t.Fatal("release-laptop help must explicitly state its non-gating diagnostic role")
	}
}

func TestLocalVPNDiagnosticsRemainAvailable(t *testing.T) {
	tests := []struct {
		path       string
		executable bool
	}{
		{path: "real-vpn.sh"},
		{path: "tun-package-cleanup.sh", executable: true},
	}

	for _, test := range tests {
		info, err := os.Stat(test.path)
		if err != nil {
			t.Fatalf("local VPN diagnostic %s must remain available: %v", test.path, err)
		}
		if !info.Mode().IsRegular() {
			t.Fatalf("local VPN diagnostic %s is not a regular file", test.path)
		}
		if test.executable && info.Mode().Perm()&0o111 == 0 {
			t.Fatalf("local VPN diagnostic %s must remain executable", test.path)
		}
	}
}
