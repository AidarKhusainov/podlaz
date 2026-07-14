package recovery

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecuteWithOptionsTreatsResolvedRevertNoSuchDeviceAsPersistentRecord(t *testing.T) {
	runner := newResolvedRecoveryRunner(
		[]resolvedRecoveryCommand{missingPodlazLink(), missingPodlazLink(), missingPodlazLink()},
		[]resolvedRecoveryCommand{resolvedLinkExists(), resolvedLinkExists()},
	)
	runner.commands["resolvectl revert podlaz0"] = []resolvedRecoveryCommand{{
		stderr:   `Failed to resolve interface "podlaz0": No such device`,
		exitCode: 1,
		err:      errors.New("exit status 1"),
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
	if cleanup.Candidate.Kind != managedDNSCandidateKind || cleanup.Status != "skipped" || !strings.Contains(cleanup.Message, "restart systemd-resolved manually") {
		t.Fatalf("expected persistent record guidance after missing-device revert, got %#v", cleanup)
	}
}
