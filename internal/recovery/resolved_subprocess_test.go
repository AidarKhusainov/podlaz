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
		{name: "Ubuntu exact exit one", scenario: "missing-ubuntu-exit-1", context: backgroundTestContext, wantStatus: "recovered"},
		{name: "non-empty stdout", scenario: "missing-stdout-exit-1", context: backgroundTestContext, wantStatus: "failed"},
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
		context  func(t *testing.T) context.Context
		wantErr  bool
	}{
		{name: "exact exit one", scenario: "missing-exit-1", context: backgroundTestContext},
		{name: "Ubuntu exact exit one", scenario: "missing-ubuntu-exit-1", context: backgroundTestContext},
		{name: "non-empty stdout", scenario: "missing-stdout-exit-1", context: backgroundTestContext, wantErr: true},
		{name: "same stderr exit two", scenario: "missing-exit-2", context: backgroundTestContext, wantErr: true},
		{name: "unrelated exit one", scenario: "unrelated-exit-1", context: backgroundTestContext, wantErr: true},
		{name: "permission denied", scenario: "permission-denied", context: backgroundTestContext, wantErr: true},
		{name: "launch error", scenario: "launch-error", context: backgroundTestContext, wantErr: true},
		{name: "signal after partial stderr", scenario: "missing-signal", context: backgroundTestContext, wantErr: true},
		{name: "oversized missing stderr", scenario: "missing-oversized", context: backgroundTestContext, wantErr: true},
		{name: "timeout after partial stderr", scenario: "missing-sleep", context: timeoutTestContext, wantErr: true},
		{name: "cancellation after partial stderr", scenario: "missing-sleep", context: cancelledTestContext, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := newResolvedSubprocessRunner(t, tt.scenario)
			err := (OSCleanupExecutor{Runner: runner}).rollbackDNS(tt.context(t), txstate.DNSRollback{
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

func TestResolvedStatusMissingRequiresValidProcessEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name     string
		scenario string
		context  func(*testing.T) context.Context
		want     bool
	}{
		{name: "Ubuntu exact exit one", scenario: "missing-ubuntu-exit-1", context: backgroundTestContext, want: true},
		{name: "timeout", scenario: "missing-sleep", context: timeoutTestContext},
		{name: "signal", scenario: "missing-signal", context: backgroundTestContext},
		{name: "launch error", scenario: "launch-error", context: backgroundTestContext},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := newResolvedSubprocessRunner(t, tc.scenario)
			ctx := tc.context(t)
			result, err := runner.Run(ctx, "resolvectl", "status", managedInterface, "--no-pager")
			if got := resolvedStatusResourceMissing(ctx, result, err); got != tc.want {
				t.Fatalf("unexpected missing classification: got %t want %t result=%#v err=%v", got, tc.want, result, err)
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
	case "missing-ubuntu-exit-1":
		_, _ = os.Stderr.WriteString(resolvedMissingDeviceIgnoringStderr + "\n")
		os.Exit(1)
	case "missing-stdout-exit-1":
		_, _ = os.Stdout.WriteString("unexpected warning\n")
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
	_, _ = os.Stderr.WriteString(resolvedMissingDeviceStderr + "\n")
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
	rawStdout := stdout.String()
	rawStderr := stderr.String()
	result := CommandResult{
		Stdout:    strings.TrimSpace(rawStdout),
		Stderr:    strings.TrimSpace(rawStderr),
		RawStdout: rawStdout,
		RawStderr: rawStderr,
	}
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
