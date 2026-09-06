package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicArtifactScanAppendsOnlySanitizedPrivateFailure(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(privateCommandDir, "004-connect-proxy-only-explicit.stderr"), []byte("podlaz: authorization denied: polkit denied "+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "./scan-public-artifacts.sh")
	cmd.Dir = "."
	cmd.Env = append(os.Environ(),
		"E2E_ARTIFACT_DIR="+artifactDir,
		"E2E_TMP_ROOT="+privateDir,
	)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("scan public artifacts: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}

	result, err := os.ReadFile(filepath.Join(artifactDir, "real-provider-result.txt"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(result)
	for _, required := range []string{
		"real-provider data-plane: failure\n",
		"command: connect-proxy-only-explicit\n",
		"class: authorization-denied\n",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("public diagnostics lost %q: %q", required, text)
		}
	}
	if strings.Contains(text, secret) || strings.Contains(stdout.String(), secret) || strings.Contains(stderr.String(), secret) {
		t.Fatal("private value leaked through public artifact scan")
	}
}

func TestPublicArtifactScanFallsBackWithoutPrivateCommandEvidence(t *testing.T) {
	artifactDir := t.TempDir()
	privateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(artifactDir, "real-provider-result.txt"), []byte("real-provider data-plane: failure\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "./scan-public-artifacts.sh")
	cmd.Dir = "."
	cmd.Env = append(os.Environ(),
		"E2E_ARTIFACT_DIR="+artifactDir,
		"E2E_TMP_ROOT="+privateDir,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("scan public artifacts without private evidence: %v\noutput=%s", err, output)
	}

	result, err := os.ReadFile(filepath.Join(artifactDir, "real-provider-result.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(result), "real-provider data-plane: failure\ncommand: unavailable\nclass: unclassified\n"; got != want {
		t.Fatalf("unexpected fallback diagnostics:\ngot:  %q\nwant: %q", got, want)
	}
}
