package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runRemnawaveArtifactScanner(t *testing.T, kind, dir string) error {
	t.Helper()
	cmd := exec.Command("bash", "scan-remnawave-artifacts.sh", kind)
	cmd.Env = append(os.Environ(), "E2E_ARTIFACT_DIR="+dir)
	return cmd.Run()
}

func writeRemnawaveReport(t *testing.T, dir, kind string) string {
	t.Helper()
	var name string
	var lines []string
	switch kind {
	case "proxy":
		name = "remnawave-proxy.txt"
		lines = []string{
			"candidate.commit=" + strings.Repeat("a", 40),
			"candidate.package_sha256=" + strings.Repeat("b", 64),
			"remnawave.panel_version=3.4.5",
			"remnawave.node_version=3.4.2",
			"remnawave.proxy_data_plane=pass",
			"remnawave.proxy_path_attribution=pass",
			"remnawave.proxy_cleanup=pass",
			"fixture.cleanup=pass",
			"failure.class=none",
			"failure.step=none",
		}
	case "subscription":
		name = "remnawave-subscription.txt"
		lines = []string{
			"candidate.commit=" + strings.Repeat("a", 40),
			"candidate.package_sha256=" + strings.Repeat("b", 64),
			"remnawave.panel_version=3.4.5",
			"remnawave.node_version=3.4.2",
			"remnawave.subscription_import=pass",
			"remnawave.subscription_refresh=pass",
			"remnawave.hwid_registration=pass",
			"remnawave.hwid_stable=pass",
			"remnawave.hwid_device_limit=pass",
			"remnawave.rejected_state_preserved=pass",
			"fixture.cleanup=pass",
			"failure.class=none",
			"failure.step=none",
		}
	default:
		t.Fatalf("unknown report kind %q", kind)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRemnawavePublicArtifactScannerIsFailClosed(t *testing.T) {
	for _, kind := range []string{"proxy", "subscription"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			report := writeRemnawaveReport(t, dir, kind)
			if err := runRemnawaveArtifactScanner(t, kind, dir); err != nil {
				t.Fatalf("normalized %s report rejected: %v", kind, err)
			}
			data, err := os.ReadFile(report)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(report, append(data, []byte("unexpected.raw=secret-provider-material\n")...), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := runRemnawaveArtifactScanner(t, kind, dir); err == nil {
				t.Fatalf("%s scanner accepted non-normalized evidence", kind)
			}
		})
	}
}

func TestRemnawavePublicArtifactScannerAcceptsNormalizedFailure(t *testing.T) {
	dir := t.TempDir()
	report := writeRemnawaveReport(t, dir, "proxy")
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(data), "remnawave.proxy_data_plane=pass", "remnawave.proxy_data_plane=fail")
	text = strings.ReplaceAll(text, "failure.class=none", "failure.class=product")
	text = strings.ReplaceAll(text, "failure.step=none", "failure.step=proxy.data_plane")
	if err := os.WriteFile(report, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runRemnawaveArtifactScanner(t, "proxy", dir); err != nil {
		t.Fatalf("normalized failure report rejected: %v", err)
	}
}

func TestRemnawavePublicArtifactScannerRejectsInconsistentFailureMetadata(t *testing.T) {
	dir := t.TempDir()
	report := writeRemnawaveReport(t, dir, "subscription")
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(data), "failure.class=none", "failure.class=fixture")
	if err := os.WriteFile(report, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runRemnawaveArtifactScanner(t, "subscription", dir); err == nil {
		t.Fatal("Remnawave scanner accepted failure metadata without failed evidence")
	}
}

func TestRemnawavePublicArtifactScannerRejectsExtraFiles(t *testing.T) {
	dir := t.TempDir()
	writeRemnawaveReport(t, dir, "proxy")
	if err := os.WriteFile(filepath.Join(dir, "raw-provider.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runRemnawaveArtifactScanner(t, "proxy", dir); err == nil {
		t.Fatal("Remnawave scanner accepted an unexpected public artifact")
	}
}

func TestEphemeralRemnawavePRWorkflowUsesMergeCandidateProvenance(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/ephemeral-remnawave.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{
		"- 'scripts/e2e/data-plane.sh'",
		"- 'scripts/e2e/scan-remnawave-artifacts.sh'",
		"CANDIDATE_COMMIT: ${{ github.sha }}",
		"test \"$(git rev-parse HEAD)\" = \"${CANDIDATE_COMMIT}\"",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("ephemeral Remnawave PR workflow lost %q", required)
		}
	}
	if strings.Contains(text, "github.event.pull_request.head.sha") {
		t.Fatal("ephemeral Remnawave PR workflow mixes PR-head provenance with merge-candidate checkout")
	}
}

func TestRemnawaveFixtureHasRetryablePollingAndVersionedPins(t *testing.T) {
	data, err := os.ReadFile("remnawave-fixture.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{
		"api_try()",
		"api_try GET \"/api/nodes/${node_uuid}\" \"\" \"${output}\" || true",
		"mask_value \"${NODE_SECRET}\"",
		"PANEL_IMAGE=\"${PODLAZ_REMNAWAVE_PANEL_IMAGE:-${REMNAWAVE_PANEL_IMAGE_DEFAULT}}\"",
		"NODE_IMAGE=\"${PODLAZ_REMNAWAVE_NODE_IMAGE:-${REMNAWAVE_NODE_IMAGE_DEFAULT}}\"",
		"remnawave_fixture_cleanup()",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("Remnawave fixture lost hardening contract %q", required)
		}
	}
	lib, err := os.ReadFile("lib/remnawave.sh")
	if err != nil {
		t.Fatal(err)
	}
	libText := string(lib)
	for _, required := range []string{
		"REMNAWAVE_PANEL_VERSION=\"3.4.5\"",
		"REMNAWAVE_NODE_VERSION=\"3.4.2\"",
		"ghcr.io/remnawave/backend:${REMNAWAVE_PANEL_VERSION}@sha256:",
		"ghcr.io/remnawave/node:${REMNAWAVE_NODE_VERSION}@sha256:",
	} {
		if !strings.Contains(libText, required) {
			t.Fatalf("Remnawave compatibility contract lost %q", required)
		}
	}
}

func TestPermanentRemnawaveWorkflowsDoNotConsumeMaintainerProviderSecrets(t *testing.T) {
	for _, path := range []string{
		"../../.github/workflows/integration.yml",
		"../../.github/workflows/release.yml",
		"../../.github/workflows/hosted-package-restart.yml",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, forbidden := range []string{
			"environment: vpn-e2e",
			"${{ secrets.PODLAZ_E2E_PROFILE_URI }}",
			"${{ secrets.PODLAZ_E2E_PROFILE_URI_LIST }}",
			"${{ secrets.PODLAZ_E2E_EXPECTED_EGRESS_IP }}",
		} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s still consumes retired maintainer provider state %q", path, forbidden)
			}
		}
	}
}
