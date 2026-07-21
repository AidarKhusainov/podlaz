package recovery

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestResolvedMissingDeviceRecoveryValidatesRealProcessOutcome(t *testing.T) {
	tests := []struct {
		name       string
		scenario   string
		context    func(t *testing.T) context.Context
		wantStatus string
	}{
		{name: "exact exit one", scenario: "missing-exit-1", context: backgroundTestContext, wantStatus: "recovered"},
		{name: "same stderr exit two", scenario: "missing-exit-2", context: backgroundTestContext, wantStatus: "failed"},
		{name: "unrelated exit one", scenario: "unrelated-exit-1", context: backgroundTestContext, wantStatus: "failed"},
		{name: "permission denied", scenario: "permission-denied", context: backgroundTestContext, wantStatus: "failed"},
		{name: "launch error", scenario: "launch-error", context: backgroundTestContext, wantStatus: "failed"},
		{name: "signal after partial stderr", scenario: "missing-signal", context: backgroundTestContext, wantStatus: "failed"},
		{name: "oversized missing stderr", scenario: "missing-oversized", context: backgroundTestContext, wantStatus: "failed"},
		{name: "timeout after partial stderr", scenario: "missing-sleep", context: timeoutTestContext, wantStatus: "failed"},
		{name: "cancellation after partial stderr", scenario: "missing-sleep", context: cancelledTestContext, wantStatus: "failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := newResolvedSubprocessRunner(t, tt.scenario)
			result := (OSCleanupExecutor{Runner: runner}).cleanupManagedResolvedLink(tt.context(t), Candidate{
				Kind:        managedDNSCandidateKind,
				Description: managedDNSDescription,
				Target:      managedInterface,
			})

			if result.Status != tt.wantStatus {
				t.Fatalf("unexpected cleanup result for %s: %#v", tt.scenario, result)
			}
			if tt.wantStatus == "recovered" {
				var exitErr *exec.ExitError
				if !errors.As(runner.lastErr, &exitErr) || exitErr.ExitCode() != 1 {
					t.Fatalf("expected exact *exec.ExitError(1), got %T: %v", runner.lastErr, runner.lastErr)
				}
			}
		})
	}
}

func TestTransactionDNSRollbackValidatesRealProcessOutcome(t *testing.T) {
	tests := []struct {
		name     string
		scenario string
		wantErr  bool
	}{
		{name: "exact exit one", scenario: "missing-exit-1"},
		{name: "same stderr exit two", scenario: "missing-exit-2", wantErr: true},
		{name: "unrelated exit one", scenario: "unrelated-exit-1", wantErr: true},
		{name: "launch error", scenario: "launch-error", wantErr: true},
		{name: "signal after partial stderr", scenario: "missing-signal", wantErr: true},
		{name: "oversized missing stderr", scenario: "missing-oversized", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := newResolvedSubprocessRunner(t, tt.scenario)
			err := (OSCleanupExecutor{Runner: runner}).rollbackDNS(context.Background(), txstate.DNSRollback{
				Backend: "systemd-resolved",
				Link:    managedInterface,
				Owner:   txstate.TransactionOwner,
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("unexpected transaction DNS rollback error for %s: %v", tt.scenario, err)
			}
		})
	}
}

func TestResolvedSubprocessHelper(t *testing.T) {
	if os.Getenv("PODLAZ_RESOLVED_HELPER") != "1" {
		return
	}
	if os.Getenv("PODLAZ_RESOLVED_TOOL") == "ip" {
		_, _ = os.Stderr.WriteString(`Device "podlaz0" does not exist.` + "\n")
		os.Exit(1)
	}

	switch os.Getenv("PODLAZ_RESOLVED_SCENARIO") {
	case "missing-exit-1":
		writeResolvedMissingStderr()
		os.Exit(1)
	case "missing-exit-2":
		writeResolvedMissingStderr()
		os.Exit(2)
	case "unrelated-exit-1":
		_, _ = os.Stderr.WriteString("unrelated cleanup failure\n")
		os.Exit(1)
	case "permission-denied":
		_, _ = os.Stderr.WriteString("Access denied\n")
		os.Exit(1)
	case "missing-signal":
		writeResolvedMissingStderr()
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		time.Sleep(time.Second)
	case "missing-oversized":
		writeResolvedMissingStderr()
		_, _ = os.Stderr.WriteString(strings.Repeat("x", maxResolvedMissingStderrSize+1))
		os.Exit(1)
	case "missing-sleep":
		writeResolvedMissingStderr()
		time.Sleep(10 * time.Second)
	default:
		os.Exit(2)
	}
}

func writeResolvedMissingStderr() {
	_, _ = os.Stderr.WriteString(`Failed to resolve interface "podlaz0": No such device` + "\n")
}

type resolvedSubprocessRunner struct {
	executable string
	scenario   string
	lastErr    error
}

func newResolvedSubprocessRunner(t *testing.T, scenario string) *resolvedSubprocessRunner {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test executable: %v", err)
	}
	return &resolvedSubprocessRunner{executable: executable, scenario: scenario}
}

func (r *resolvedSubprocessRunner) LookPath(file string) (string, error) {
	return filepath.Join(filepath.Dir(r.executable), file), nil
}

func (r *resolvedSubprocessRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	executable := r.executable
	if r.scenario == "launch-error" && filepath.Base(name) == "resolvectl" {
		executable = filepath.Join(filepath.Dir(r.executable), "podlaz-missing-resolvectl-helper")
	}
	cmd := exec.CommandContext(ctx, executable, "-test.run=TestResolvedSubprocessHelper", "--")
	cmd.Env = append(os.Environ(),
		"PODLAZ_RESOLVED_HELPER=1",
		"PODLAZ_RESOLVED_TOOL="+filepath.Base(name),
		"PODLAZ_RESOLVED_SCENARIO="+r.scenario,
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	r.lastErr = err
	result := CommandResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
	} else {
		result.ExitCode = -1
	}
	return result, err
}

func backgroundTestContext(*testing.T) context.Context {
	return context.Background()
}

func timeoutTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	t.Cleanup(cancel)
	return ctx
}

func cancelledTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	time.AfterFunc(50*time.Millisecond, cancel)
	return ctx
}
