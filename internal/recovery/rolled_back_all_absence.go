package recovery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

// VerifyAllRolledBackTransactionAbsence proves the exact footprint of every
// durable rolled-back transaction absent without turning those tombstones into
// cleanup authority. It is used when a terminal replay performed no candidate
// mutation of its own but prior completed rollbacks may still carry exact
// observation evidence.
func VerifyAllRolledBackTransactionAbsence(ctx context.Context, runtimeDir string) error {
	return verifyAllRolledBackTransactionAbsenceWithOptions(ctx, runtimeDir, rolledBackTransactionAbsenceOptions{})
}

func verifyAllRolledBackTransactionAbsenceWithOptions(
	ctx context.Context,
	runtimeDir string,
	opts rolledBackTransactionAbsenceOptions,
) error {
	if ctx == nil || ctx.Err() != nil {
		return errors.New("rolled-back transaction absence proof requires a live context")
	}
	runtimeDir = runtimeDirOrDefault(runtimeDir)
	if opts.Runner == nil {
		opts.Runner = OSRunner{}
	}
	if opts.PathExists == nil {
		opts.PathExists = lstatPathExists
	}
	if opts.ReadFile == nil {
		opts.ReadFile = os.ReadFile
	}

	summaries, warnings := txstate.ScanTransactions(runtimeDir)
	if len(warnings) != 0 {
		return fmt.Errorf("transaction authority inspection is inconclusive: %s", strings.Join(warnings, "; "))
	}
	store := txstate.TransactionStore{RuntimeDir: runtimeDir}
	for _, summary := range summaries {
		if summary.RequiresRecovery {
			return fmt.Errorf("durable transaction authority remains: %s (%s)", summary.ID, summary.State)
		}
		if summary.State != txstate.TransactionRolledBack {
			return fmt.Errorf("transaction %s has unexpected non-recovery state %s", summary.ID, summary.State)
		}
		tx, _, err := store.Load(summary.ID)
		if err != nil {
			return fmt.Errorf("load rolled-back transaction evidence %s: %w", summary.ID, err)
		}
		if tx.State != txstate.TransactionRolledBack || tx.RequiresRecovery() {
			return fmt.Errorf("transaction %s regained cleanup authority", summary.ID)
		}
		if err := verifyRolledBackTransactionFootprintAbsent(ctx, runtimeDir, tx, opts); err != nil {
			return fmt.Errorf("rolled-back transaction %s absence proof failed: %w", summary.ID, err)
		}
	}
	return nil
}
