package doctor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const defaultCommandTimeout = 3 * time.Second

// CommandResult contains a completed command's observable output.
type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// CommandRunner is the read-only command execution abstraction used by doctor.
type CommandRunner interface {
	LookPath(file string) (string, error)
	Run(ctx context.Context, name string, args ...string) (CommandResult, error)
}

// OSRunner executes read-only host commands through os/exec.
type OSRunner struct{}

// LookPath resolves a command using the host PATH.
func (OSRunner) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

// Run executes a host command and captures stdout, stderr, and exit code.
func (OSRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := CommandResult{
		Stdout:   strings.TrimSpace(stdout.String()),
		Stderr:   strings.TrimSpace(stderr.String()),
		ExitCode: 0,
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

func runCommand(ctx context.Context, runner CommandRunner, name string, args ...string) (CommandResult, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, defaultCommandTimeout)
	defer cancel()

	return runner.Run(cmdCtx, name, args...)
}

func commandSucceeded(result CommandResult, err error) bool {
	return err == nil && result.ExitCode == 0
}

func commandFailedUnexpectedly(result CommandResult, err error) bool {
	return err != nil || result.ExitCode != 0
}

func resourceMissing(result CommandResult) bool {
	if result.ExitCode == 0 {
		return false
	}
	text := strings.ToLower(result.Stdout + " " + result.Stderr)
	return strings.Contains(text, "does not exist") ||
		strings.Contains(text, "cannot find device") ||
		strings.Contains(text, "no such file or directory") ||
		strings.Contains(text, "no such table")
}

func commandFailureMessage(result CommandResult, err error) string {
	parts := make([]string, 0, 3)
	if result.ExitCode >= 0 {
		parts = append(parts, fmt.Sprintf("exit code %d", result.ExitCode))
	}
	if result.Stderr != "" {
		parts = append(parts, "stderr: "+singleLine(result.Stderr))
	}
	if err != nil && result.Stderr == "" {
		parts = append(parts, err.Error())
	}
	if len(parts) == 0 {
		return "command failed"
	}
	return strings.Join(parts, ", ")
}

func singleLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
