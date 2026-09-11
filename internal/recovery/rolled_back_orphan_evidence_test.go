package recovery

import (
	"context"
	"strings"
	"testing"
	"time"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestRolledBackTransactionAbsenceVerifiesOrphanEvidenceWithoutTreatingItAsAuthority(t *testing.T) {
	for _, tc := range []struct {
		name               string
		orphanRoutePresent bool
		wantErr             string
	}{
		{name: "clean orphan", orphanRoutePresent: false},
		{name: "orphan residue", orphanRoutePresent: true, wantErr: "exact route remains present: 128.0.0.0/1 table 51820"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtimeDir := t.TempDir()
			retained := saveRolledBackAbsenceEvidence(t, runtimeDir)
			saveOrphanRolledBackRouteEvidence(t, runtimeDir)
			runner := orphanRolledBackEvidenceRunner{
				terminalAbsenceRunner: terminalAbsenceRunner{},
				orphanRoutePresent:    tc.orphanRoutePresent,
			}

			err := verifyRolledBackTransactionAbsenceWithOptions(
				context.Background(),
				runtimeDir,
				retained.ID,
				rolledBackTransactionAbsenceOptions{
					Runner:     runner,
					PathExists: func(string) (bool, error) { return false, nil },
					ReadFile:   func(string) ([]byte, error) { return nil, errProcessNotFoundForAbsenceTest },
				},
			)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("clean orphan rolled-back evidence blocked exact absence proof: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("orphan residue error=%v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func saveOrphanRolledBackRouteEvidence(t *testing.T, runtimeDir string) {
	t.Helper()
	rollback := txstate.RollbackMetadata{
		Routes: []txstate.RouteRollback{{
			Table: "51820",
			CIDR:  "128.0.0.0/1",
			Dev:   managedInterface,
			Owner: netexecutor.OwnerRoute,
		}},
	}
	tx := txstate.NewTransaction("tx-orphan-rolled-back", "profile-1", "tun", time.Now().UTC())
	tx.State = txstate.TransactionRolledBack
	tx.Rollback = rollback
	tx.DesiredPlan = desiredPlanForRollback(rollback)
	tx.AppliedSteps = appliedStepsForRollback(rollback, time.Now().UTC())
	if _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Save(tx); err != nil {
		t.Fatalf("save orphan rolled-back transaction evidence: %v", err)
	}
}

type orphanRolledBackEvidenceRunner struct {
	terminalAbsenceRunner
	orphanRoutePresent bool
}

func (r orphanRolledBackEvidenceRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	key := strings.TrimSpace(strings.Join(append([]string{name}, args...), " "))
	if strings.HasSuffix(key, "ip -4 route show table 51820 128.0.0.0/1") ||
		strings.Contains(key, " -4 route show table 51820 128.0.0.0/1") {
		if r.orphanRoutePresent {
			return CommandResult{Stdout: "128.0.0.0/1 dev podlaz0 table 51820", ExitCode: 0}, nil
		}
		return CommandResult{ExitCode: 0}, nil
	}
	return r.terminalAbsenceRunner.Run(ctx, name, args...)
}

var errProcessNotFoundForAbsenceTest = processNotFoundError{}

type processNotFoundError struct{}

func (processNotFoundError) Error() string { return "process not found" }
func (processNotFoundError) Is(target error) bool {
	return target != nil && target.Error() == "file does not exist"
}
