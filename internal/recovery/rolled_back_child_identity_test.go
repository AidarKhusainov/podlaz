package recovery

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestRolledBackTransactionAbsenceTreatsReusedPIDAsOriginalChildAbsent(t *testing.T) {
	runtimeDir := t.TempDir()
	tx := saveRolledBackAbsenceEvidence(t, runtimeDir)
	tx.Rollback.ChildProcesses[0].StartTime = "111111"
	if _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Save(tx); err != nil {
		t.Fatal(err)
	}

	err := verifyRolledBackTransactionAbsenceWithOptions(
		context.Background(),
		runtimeDir,
		tx.ID,
		rolledBackTransactionAbsenceOptions{
			Runner:     terminalAbsenceRunner{},
			PathExists: func(string) (bool, error) { return false, nil },
			ReadFile: func(path string) ([]byte, error) {
				if path == "/proc/4242/stat" {
					return processStatForAbsenceTest(4242, "222222"), nil
				}
				return nil, os.ErrNotExist
			},
		},
	)
	if err != nil {
		t.Fatalf("PID reuse must prove the original tracked child absent: %v", err)
	}
}

func TestRolledBackTransactionAbsenceRejectsSameTrackedChildIdentity(t *testing.T) {
	runtimeDir := t.TempDir()
	tx := saveRolledBackAbsenceEvidence(t, runtimeDir)
	tx.Rollback.ChildProcesses[0].StartTime = "111111"
	if _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Save(tx); err != nil {
		t.Fatal(err)
	}

	err := verifyRolledBackTransactionAbsenceWithOptions(
		context.Background(),
		runtimeDir,
		tx.ID,
		rolledBackTransactionAbsenceOptions{
			Runner:     terminalAbsenceRunner{},
			PathExists: func(string) (bool, error) { return false, nil },
			ReadFile: func(path string) ([]byte, error) {
				if path == "/proc/4242/stat" {
					return processStatForAbsenceTest(4242, "111111"), nil
				}
				return nil, os.ErrNotExist
			},
		},
	)
	if err == nil {
		t.Fatal("same PID and start-time must prove the tracked child is still present")
	}
}

func TestRolledBackTransactionAbsenceRejectsMissingTrackedChildStartTime(t *testing.T) {
	runtimeDir := t.TempDir()
	tx := saveRolledBackAbsenceEvidence(t, runtimeDir)
	tx.Rollback.ChildProcesses[0].StartTime = ""
	if _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Save(tx); err != nil {
		t.Fatal(err)
	}

	err := verifyRolledBackTransactionAbsenceWithOptions(
		context.Background(),
		runtimeDir,
		tx.ID,
		rolledBackTransactionAbsenceOptions{
			Runner:     terminalAbsenceRunner{},
			PathExists: func(string) (bool, error) { return false, nil },
			ReadFile:   func(string) ([]byte, error) { return nil, os.ErrNotExist },
		},
	)
	if err == nil {
		t.Fatal("missing durable child start-time must make terminal proof inconclusive")
	}
}

func processStatForAbsenceTest(pid int, startTime string) []byte {
	fields := make([]string, 20)
	fields[0] = "S"
	for i := 1; i < len(fields); i++ {
		fields[i] = "0"
	}
	fields[19] = startTime
	return []byte(fmt.Sprintf("%d (xray worker) %s\n", pid, strings.Join(fields, " ")))
}

var _ = filepath.Clean
