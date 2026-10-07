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
			if err := os.WriteFile(report, append(data, []byte("unexpected.raw=value\n")...), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := runRemnawaveArtifactScanner(t, kind, dir); err == nil {
				t.Fatalf("%s scanner accepted non-normalized evidence", kind)
			}
		})
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
