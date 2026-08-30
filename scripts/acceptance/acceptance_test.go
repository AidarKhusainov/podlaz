package acceptance

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReleaseAcceptancePythonSuite(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve acceptance test path")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	cmd := exec.Command("python3", "-m", "unittest", "discover", "-s", "scripts/acceptance/tests", "-p", "test_*.py", "-v")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "PYTHONPATH="+filepath.Join(repoRoot, "scripts", "acceptance"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("acceptance Python suite failed: %v\n%s", err, output)
	}
}
