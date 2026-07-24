package recovery

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

func TestExecuteWithOptionsTreatsResolvedRevertNoSuchDeviceAsRecovered(t *testing.T) {
	runner := newResolvedRecoveryRunner(
		[]resolvedRecoveryCommand{missingPodlazLink(), missingPodlazLink(), missingPodlazLink()},
		[]resolvedRecoveryCommand{resolvedLinkExists()},
	)
	runner.commands["resolvectl revert podlaz0"] = []resolvedRecoveryCommand{{
		stderr:   `Failed to resolve interface "podlaz0": No such device` + "\n",
		exitCode: 1,
		err:      resolvedTestExitError{code: 1},
	}}
	runtimeDir := filepath.Join(t.TempDir(), "podlaz")

	result := ExecuteWithOptions(context.Background(), Options{
		Runner:     runner,
		RuntimeDir: runtimeDir,
		Executor:   DaemonCleanupExecutor{Runner: runner, RuntimeDir: runtimeDir},
	})

	if len(result.Results) != 1 {
		t.Fatalf("expected one cleanup result, got %#v", result.Results)
	}
	cleanup := result.Results[0]
	if cleanup.Candidate.Kind != managedDNSCandidateKind || cleanup.Status != "recovered" || cleanup.Message != "" {
		t.Fatalf("expected idempotent recovered result after missing-device revert, got %#v", cleanup)
	}
	if result.HasFailures() || result.HasIncompleteCleanup() {
		t.Fatalf("missing-device cleanup must be complete: %#v", result)
	}
}

type resolvedTestExitError struct {
	code int
}

func (e resolvedTestExitError) Error() string {
	return fmt.Sprintf("exit status %d", e.code)
}

func (e resolvedTestExitError) ExitCode() int {
	return e.code
}
