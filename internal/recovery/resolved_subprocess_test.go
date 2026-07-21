package recovery

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestResolvedMissingDeviceRecoveryHandlesRealExitError(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test executable: %v", err)
	}
	runner := &resolvedSubprocessRunner{executable: executable}
	executor := OSCleanupExecutor{Runner: runner}

	result := executor.cleanupManagedResolvedLink(context.Background(), Candidate{
		Kind:        managedDNSCandidateKind,
		Description: managedDNSDescription,
		Target:      managedInterface,
	})

	if result.Status != "recovered" || result.Message != "" {
		t.Fatalf("expected recovered result for a real missing-device exit, got %#v", result)
	}
	var exitErr *exec.ExitError
	if !errors.As(runner.lastErr, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("expected the runner boundary to observe *exec.ExitError(1), got %T: %v", runner.lastErr, runner.lastErr)
	}
}

func TestResolvedSubprocessHelper(t *testing.T) {
	if os.Getenv("PODLAZ_RESOLVED_HELPER") != "1" {
		return
	}
	switch os.Getenv("PODLAZ_RESOLVED_TOOL") {
	case "ip":
		_, _ = os.Stderr.WriteString("Device podlaz0 does not exist\n")
		os.Exit(1)
	case "resolvectl":
		_, _ = os.Stderr.WriteString(`Failed to resolve interface "podlaz0": No such device` + "\n")
		os.Exit(1)
	default:
		os.Exit(2)
	}
}

type resolvedSubprocessRunner struct {
	executable string
	lastErr    error
}

func (r *resolvedSubprocessRunner) LookPath(file string) (string, error) {
	return filepath.Join(filepath.Dir(r.executable), file), nil
}

func (r *resolvedSubprocessRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, r.executable, "-test.run=TestResolvedSubprocessHelper", "--")
	cmd.Env = append(os.Environ(),
		"PODLAZ_RESOLVED_HELPER=1",
		"PODLAZ_RESOLVED_TOOL="+filepath.Base(name),
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
	}
	return result, err
}
