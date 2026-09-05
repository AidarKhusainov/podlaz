package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateCommandCaptureKeepsOutputOffConsoleAndPreservesExitCode(t *testing.T) {
	artifactDir := t.TempDir()
	privateDir := t.TempDir()
	script := `
set -Eeuo pipefail
source ./lib/e2e.sh
source ./lib/private_command.sh
set +e
capture_private_command private-fixture bash -c 'printf "private-stdout-marker\n"; printf "private-stderr-marker\n" >&2; exit 7'
code=$?
set -e
[[ "${code}" == "7" ]]
grep -Fx 'private-stdout-marker' "${LAST_STDOUT}" >/dev/null
grep -Fx 'private-stderr-marker' "${LAST_STDERR}" >/dev/null
[[ "${LAST_STDOUT}" == "${E2E_TMP_ROOT}"/* ]]
[[ "${LAST_STDERR}" == "${E2E_TMP_ROOT}"/* ]]
printf 'capture-verified\n'
`
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(),
		"E2E_ARTIFACT_DIR="+artifactDir,
		"E2E_TMP_ROOT="+privateDir,
	)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("private command capture contract failed: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	for _, leaked := range []string{"private-stdout-marker", "private-stderr-marker"} {
		if strings.Contains(stdout.String(), leaked) || strings.Contains(stderr.String(), leaked) {
			t.Fatalf("private command output leaked to console: %q", leaked)
		}
	}
	if !strings.Contains(stdout.String(), "capture-verified") {
		t.Fatalf("private capture fixture did not complete: stdout=%q", stdout.String())
	}
	matches, err := filepath.Glob(filepath.Join(privateDir, "private-command", "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Fatalf("expected private stdout/stderr files only, got %v", matches)
	}
}
