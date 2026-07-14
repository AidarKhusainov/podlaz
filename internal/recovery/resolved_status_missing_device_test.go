package recovery

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestPlanWithOptionsTreatsResolvedStatusNoSuchDeviceAsMissing(t *testing.T) {
	runner := newResolvedRecoveryRunner(
		[]resolvedRecoveryCommand{missingPodlazLink(), missingPodlazLink()},
		[]resolvedRecoveryCommand{{
			stderr:   `Failed to resolve interface "podlaz0": No such device`,
			exitCode: 1,
			err:      errors.New("exit status 1"),
		}},
	)

	plan := PlanWithOptions(context.Background(), Options{
		Runner:     runner,
		RuntimeDir: filepath.Join(t.TempDir(), "podlaz"),
	})

	assertNoCandidateKind(t, plan, managedDNSCandidateKind)
	if len(plan.Warnings) != 0 {
		t.Fatalf("missing resolved status must not produce an inspection warning: %#v", plan.Warnings)
	}
}
