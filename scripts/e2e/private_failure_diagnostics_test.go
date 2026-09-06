package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizedPrivateFailureKeepsReasonWithoutPrivateValues(t *testing.T) {
	privateDir := t.TempDir()
	privateCommandDir := filepath.Join(privateDir, "private-command")
	if err := os.MkdirAll(privateCommandDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const secret = "private-endpoint.example.invalid"
	stderrPath := filepath.Join(privateCommandDir, "004-connect-proxy-only-explicit.stderr")
	if err := os.WriteFile(stderrPath, []byte("Error: authorization denied for "+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "./sanitize-private-failure.sh", "connect-proxy-only-explicit", stderrPath)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(),
		"E2E_TMP_ROOT="+privateDir,
		"PODLAZ_E2E_PROFILE_URI=vless://user@"+secret+":443",
	)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("sanitize private failure: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}

	text := stdout.String()
	if !strings.Contains(text, "command: connect-proxy-only-explicit") {
		t.Fatalf("sanitized diagnostics lost command identity: %q", text)
	}
	if !strings.Contains(text, "class: authorization") {
		t.Fatalf("sanitized diagnostics lost failure class: %q", text)
	}
	if strings.Contains(text, secret) || strings.Contains(stderr.String(), secret) {
		t.Fatal("private value leaked from sanitized failure diagnostics")
	}
}
