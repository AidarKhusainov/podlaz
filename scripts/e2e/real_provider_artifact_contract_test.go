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
		scan := strings.Index(text, "Scan public artifacts")
		cleanup := strings.Index(text, "Remove private E2E temp state")
		if scan < 0 || cleanup < 0 || scan >= cleanup {
			t.Fatalf("%s must scan sanitized public diagnostics before private temp cleanup", workflow)
		}
	}

	scanner, err := os.ReadFile("scan-public-artifacts.sh")
	if err != nil {
		t.Fatalf("read public artifact scanner: %v", err)
	}
	for _, required := range []string{"lib/tun_soak_metrics.py", "classify-cli-error", "failed-command", "step: %s"} {
		if !strings.Contains(string(scanner), required) {
			t.Fatalf("public artifact scanner must preserve bounded failure evidence %q", required)
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

func runPublicArtifactGateWithPrivateRoot(t *testing.T, artifactDir, privateDir string) (string, string, error) {
	t.Helper()
	cmd := exec.Command("bash", "scan-public-artifacts.sh")
	cmd.Env = append(os.Environ(),
		"E2E_ARTIFACT_DIR="+artifactDir,
		"E2E_TMP_ROOT="+privateDir,
	)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
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

func TestPublicArtifactGateKeepsPrivateFailureDiagnosticSafe(t *testing.T) {
	artifactDir := t.TempDir()
	privateDir := t.TempDir()
	privateCommandDir := filepath.Join(privateDir, "private-command")
	if err := os.MkdirAll(privateCommandDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const secret = "private-endpoint.example.invalid"
	if err := os.WriteFile(filepath.Join(artifactDir, "real-provider-result.txt"), []byte("real-provider data-plane: failure\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stderrName := "004-connect-proxy-only-explicit.stderr"
	if err := os.WriteFile(filepath.Join(privateCommandDir, stderrName), []byte("podlaz: authorization denied: polkit denied "+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(privateCommandDir, "failed-command"), []byte(stderrName+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runPublicArtifactGateWithPrivateRoot(t, artifactDir, privateDir)
	if err != nil {
		t.Fatalf("scan public artifacts: %v\nstdout=%s\nstderr=%s", err, stdout, stderr)
	}
	result, err := os.ReadFile(filepath.Join(artifactDir, "real-provider-result.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(result), "real-provider data-plane: failure\nstep: connect-proxy-only-explicit\nclass: authorization-denied\n"; got != want {
		t.Fatalf("unexpected sanitized diagnostics:\ngot:  %q\nwant: %q", got, want)
	}
	if strings.Contains(string(result), secret) || strings.Contains(stdout, secret) || strings.Contains(stderr, secret) {
		t.Fatal("private value leaked through public artifact gate")
	}
}

func TestPublicArtifactGateUsesGenericDataPlaneStepWithoutPrivateCommandEvidence(t *testing.T) {
	artifactDir := t.TempDir()
	privateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(artifactDir, "real-provider-result.txt"), []byte("real-provider data-plane: failure\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runPublicArtifactGateWithPrivateRoot(t, artifactDir, privateDir)
	if err != nil {
		t.Fatalf("scan public artifacts without private command evidence: %v\nstdout=%s\nstderr=%s", err, stdout, stderr)
	}
	result, err := os.ReadFile(filepath.Join(artifactDir, "real-provider-result.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(result), "real-provider data-plane: failure\nstep: data-plane\nclass: unclassified\n"; got != want {
		t.Fatalf("unexpected generic diagnostics:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestPublicArtifactGateIsIdempotentForFailureResult(t *testing.T) {
	artifactDir := t.TempDir()
	privateDir := t.TempDir()
	privateCommandDir := filepath.Join(privateDir, "private-command")
	if err := os.MkdirAll(privateCommandDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir, "real-provider-result.txt"), []byte("real-provider data-plane: failure\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stderrName := "004-connect-proxy-only-explicit.stderr"
	if err := os.WriteFile(filepath.Join(privateCommandDir, stderrName), []byte("daemon is unavailable\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(privateCommandDir, "failed-command"), []byte(stderrName+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for pass := 1; pass <= 2; pass++ {
		stdout, stderr, err := runPublicArtifactGateWithPrivateRoot(t, artifactDir, privateDir)
		if err != nil {
			t.Fatalf("scan pass %d: %v\nstdout=%s\nstderr=%s", pass, err, stdout, stderr)
		}
	}
	result, err := os.ReadFile(filepath.Join(artifactDir, "real-provider-result.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(result), "real-provider data-plane: failure\nstep: connect-proxy-only-explicit\nclass: daemon-unavailable\n"; got != want {
		t.Fatalf("idempotent result mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}
