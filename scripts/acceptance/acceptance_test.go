package acceptance

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReleaseAcceptanceStandaloneSuite(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve acceptance test path")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	cmd := exec.Command("bash", "scripts/acceptance/tests/run.sh")
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("acceptance standalone suite failed: %v\n%s", err, output)
	}
}
