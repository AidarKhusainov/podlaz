package executor

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestResolvedDNSExecutorApplyValidatesInitialRevertProcessOutcome(t *testing.T) {
	tests := []struct {
		name     string
		scenario string
		context  func(*testing.T) context.Context
		wantErr  bool
	}{
		{name: "exact exit one", scenario: "missing-exit-1", context: executorBackgroundContext},
		{name: "Ubuntu exact exit one", scenario: "missing-ubuntu-exit-1", context: executorBackgroundContext},
		{name: "non-empty stdout", scenario: "missing-stdout", context: executorBackgroundContext, wantErr: true},
		{name: "same stderr exit two", scenario: "missing-exit-2", context: executorBackgroundContext, wantErr: true},
		{name: "permission denied", scenario: "permission-denied", context: executorBackgroundContext, wantErr: true},
		{name: "signal after partial stderr", scenario: "missing-signal", context: executorBackgroundContext, wantErr: true},
		{name: "timeout after partial stderr", scenario: "missing-sleep", context: executorTimeoutContext, wantErr: true},
		{name: "cancellation after partial stderr", scenario: "missing-sleep", context: executorCancelledContext, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := newExecutorResolvedSubprocessRunner(t, tt.scenario)
			executor := ResolvedDNSExecutor{Runner: runner, ApplyAttempts: 1}
			_, err := executor.Apply(tt.context(t), dnsPlanForTest())
			if (err != nil) != tt.wantErr {
				t.Fatalf("unexpected Apply result for %s: %v", tt.scenario, err)
			}
		})
	}
}

func TestResolvedDNSExecutorRollbackValidatesProcessOutcome(t *testing.T) {
	tests := []struct {
		name     string
		scenario string
		context  func(*testing.T) context.Context
		wantErr  bool
	}{
		{name: "exact exit one", scenario: "missing-exit-1", context: executorBackgroundContext},
		{name: "Ubuntu exact exit one", scenario: "missing-ubuntu-exit-1", context: executorBackgroundContext},
		{name: "non-empty stdout", scenario: "missing-stdout", context: executorBackgroundContext, wantErr: true},
		{name: "same stderr exit two", scenario: "missing-exit-2", context: executorBackgroundContext, wantErr: true},
		{name: "permission denied", scenario: "permission-denied", context: executorBackgroundContext, wantErr: true},
		{name: "signal after partial stderr", scenario: "missing-signal", context: executorBackgroundContext, wantErr: true},
		{name: "timeout after partial stderr", scenario: "missing-sleep", context: executorTimeoutContext, wantErr: true},
		{name: "cancellation after partial stderr", scenario: "missing-sleep", context: executorCancelledContext, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := newExecutorResolvedSubprocessRunner(t, tt.scenario)
			err := (ResolvedDNSExecutor{Runner: runner}).Rollback(tt.context(t), dnsPlanForTest())
			if (err != nil) != tt.wantErr {
				t.Fatalf("unexpected Rollback result for %s: %v", tt.scenario, err)
			}
		})
	}
}

func TestExecutorResolvedSubprocessHelper(t *testing.T) {
	if os.Getenv("PODLAZ_EXECUTOR_RESOLVED_HELPER") != "1" {
		return
	}

	switch os.Getenv("PODLAZ_EXECUTOR_RESOLVED_SCENARIO") {
	case "missing-exit-1":
		writeExecutorResolvedMissingStderr()
		os.Exit(1)
	case "missing-ubuntu-exit-1":
		_, _ = os.Stderr.WriteString(resolvedMissingLinkIgnoringStderr + "\n")
		os.Exit(1)
	case "missing-stdout":
		_, _ = os.Stdout.WriteString("unexpected warning\n")
		writeExecutorResolvedMissingStderr()
		os.Exit(1)
	case "missing-exit-2":
		writeExecutorResolvedMissingStderr()
		os.Exit(2)
	case "permission-denied":
		_, _ = os.Stderr.WriteString("Access denied\n")
		os.Exit(1)
	case "missing-signal":
		writeExecutorResolvedMissingStderr()
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		time.Sleep(time.Second)
	case "missing-sleep":
		writeExecutorResolvedMissingStderr()
		time.Sleep(10 * time.Second)
	default:
		os.Exit(2)
	}
}

func writeExecutorResolvedMissingStderr() {
	_, _ = os.Stderr.WriteString(`Failed to resolve interface "podlaz0": No such device` + "\n")
}

type executorResolvedSubprocessRunner struct {
	executable string
	scenario   string
}

func newExecutorResolvedSubprocessRunner(t *testing.T, scenario string) *executorResolvedSubprocessRunner {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test executable: %v", err)
	}
	return &executorResolvedSubprocessRunner{executable: executable, scenario: scenario}
}

func (r *executorResolvedSubprocessRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	if name != "resolvectl" || len(args) == 0 || args[0] != "revert" {
		return CommandResult{}, nil
	}
	cmd := exec.CommandContext(ctx, r.executable, "-test.run=TestExecutorResolvedSubprocessHelper", "--")
	cmd.Env = append(os.Environ(),
		"PODLAZ_EXECUTOR_RESOLVED_HELPER=1",
		"PODLAZ_EXECUTOR_RESOLVED_SCENARIO="+r.scenario,
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
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

func executorBackgroundContext(*testing.T) context.Context {
	return context.Background()
}

func executorTimeoutContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	t.Cleanup(cancel)
	return ctx
}

func executorCancelledContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	time.AfterFunc(50*time.Millisecond, cancel)
	return ctx
}

var _ CommandRunner = (*executorResolvedSubprocessRunner)(nil)
