package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRealProviderUploadsRequirePrivateExecutionAndFailClosedPublication(t *testing.T) {
	for _, workflow := range []string{
		"../../.github/workflows/integration.yml",
		"../../.github/workflows/release.yml",
	} {
		data, err := os.ReadFile(workflow)
		if err != nil {
			t.Fatalf("read %s: %v", workflow, err)
		}
		text := string(data)
		for _, required := range []string{
			"E2E_ARTIFACT_DIR: ${{ runner.temp }}/podlaz-e2e-tmp/real-provider-artifacts",
			"id: data_plane",
			"real-provider-result.txt",
			"bash scripts/e2e/scan-public-artifacts.sh",
			"id: evidence_scan",
			"id: private_cleanup",
			"steps.evidence_scan.outcome == 'success'",
			"steps.private_cleanup.outcome == 'success'",
		} {
			if !strings.Contains(text, required) {
				t.Fatalf("%s real-provider artifact gate lost %q", workflow, required)
			}
		}
	}
}

func runPublicArtifactGate(t *testing.T, artifactDir string) error {
	t.Helper()
	cmd := exec.Command("bash", "scan-public-artifacts.sh")
	cmd.Env = append(os.Environ(),
		"E2E_ARTIFACT_DIR="+artifactDir,
		"E2E_TMP_ROOT="+t.TempDir(),
	)
	return cmd.Run()
}

func writeSafePublicResult(t *testing.T, artifactDir string) {
	t.Helper()
	resultPath := filepath.Join(artifactDir, "real-provider-result.txt")
	if err := os.WriteFile(resultPath, []byte("real-provider data-plane: success\n"), 0o600); err != nil {
		t.Fatalf("write safe result: %v", err)
	}
}

func TestPublicArtifactGateRejectsUnexpectedFiles(t *testing.T) {
	artifactDir := t.TempDir()
	writeSafePublicResult(t, artifactDir)
	if err := runPublicArtifactGate(t, artifactDir); err != nil {
		t.Fatalf("safe public artifact staging was rejected: %v", err)
	}

	if err := os.WriteFile(filepath.Join(artifactDir, "unexpected.txt"), []byte("private evidence\n"), 0o600); err != nil {
		t.Fatalf("write unexpected artifact: %v", err)
	}
	if err := runPublicArtifactGate(t, artifactDir); err == nil {
		t.Fatal("public artifact gate accepted an unexpected extra file")
	}
}

func TestPublicArtifactGateRejectsNestedFiles(t *testing.T) {
	artifactDir := t.TempDir()
	writeSafePublicResult(t, artifactDir)
	nestedDir := filepath.Join(artifactDir, "nested")
	if err := os.Mkdir(nestedDir, 0o700); err != nil {
		t.Fatalf("create nested public artifact directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "raw.txt"), []byte("private evidence\n"), 0o600); err != nil {
		t.Fatalf("write nested public artifact: %v", err)
	}
	if err := runPublicArtifactGate(t, artifactDir); err == nil {
		t.Fatal("public artifact gate accepted nested public evidence")
	}
}
