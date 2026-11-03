package e2e

import (
	"os"
	"strings"
	"testing"
)

// GitHub evaluates job env before allocating the runner. Runner context is
// available only inside steps; a valid YAML document alone does not prove this.
func TestBootReplayDiagnosticRunnerPathsAreStepScoped(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/boot-replay-diagnostic.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	steps := strings.Index(text, "    steps:\n")
	if steps < 0 {
		t.Fatal("missing diagnostic job steps")
	}
	if strings.Contains(text[:steps], "runner.") {
		t.Fatal("runner context is unavailable in job-level env")
	}
	for _, name := range []string{
		"Prepare private guest workspace",
		"Observe bounded restarts in one disposable guest",
		"Validate public report",
		"Remove private VM state",
	} {
		start := strings.Index(text, "      - name: "+name+"\n")
		if start < 0 {
			t.Fatalf("missing step %q", name)
		}
		block := strings.SplitN(text[start+1:], "\n      - name:", 2)[0]
		if !strings.Contains(block, "E2E_TMP_ROOT: ${{ runner.temp }}/podlaz-boot-replay-private") {
			t.Fatalf("step %q must use the same private runner workspace", name)
		}
		if name != "Remove private VM state" && !strings.Contains(block, "E2E_ARTIFACT_DIR: ${{ runner.temp }}/podlaz-boot-replay-public") {
			t.Fatalf("step %q must use the same public report workspace", name)
		}
	}
}
