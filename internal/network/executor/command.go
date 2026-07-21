package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const defaultCommandTimeout = 5 * time.Second

type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) (CommandResult, error)
}

type OSRunner struct{}

func (OSRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := CommandResult{
		Stdout: strings.TrimSpace(stdout.String()),
		Stderr: strings.TrimSpace(stderr.String()),
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

func runCommand(ctx context.Context, runner CommandRunner, name string, args ...string) error {
	_, err := observeCommand(ctx, runner, name, args...)
	return err
}

func observeCommand(ctx context.Context, runner CommandRunner, name string, args ...string) (CommandResult, error) {
	if runner == nil {
		runner = OSRunner{}
	}
	cmdCtx, cancel := context.WithTimeout(ctx, defaultCommandTimeout)
	defer cancel()
	result, err := runner.Run(cmdCtx, name, args...)
	if err == nil && result.ExitCode == 0 {
		if validateErr := validateObservedCommandResult(name, args, result); validateErr == nil {
			return result, nil
		} else {
			err = validateErr
		}
	}
	return result, commandError{
		name:       name,
		args:       args,
		result:     result,
		err:        err,
		parentErr:  ctx.Err(),
		contextErr: cmdCtx.Err(),
	}
}

type commandError struct {
	name       string
	args       []string
	result     CommandResult
	err        error
	parentErr  error
	contextErr error
}

func (e commandError) Error() string {
	parts := []string{e.name + " " + strings.Join(e.args, " ")}
	if e.result.ExitCode != 0 {
		parts = append(parts, fmt.Sprintf("exit code %d", e.result.ExitCode))
	}
	if strings.TrimSpace(e.result.Stderr) != "" {
		parts = append(parts, "stderr: "+strings.TrimSpace(e.result.Stderr))
	}
	if e.err != nil && strings.TrimSpace(e.result.Stderr) == "" {
		parts = append(parts, e.err.Error())
	}
	return strings.Join(parts, ": ")
}

func (e commandError) Unwrap() error {
	return e.err
}

func flushIPv4RouteCache(ctx context.Context, runner CommandRunner) error {
	return runCommand(ctx, runner, "ip", "-4", "route", "flush", "cache")
}

func resourceMissing(err error) bool {
	var commandErr commandError
	if errors.As(err, &commandErr) && commandErr.name == "resolvectl" {
		return resolvedCommandFailureIsMissing(err)
	}
	return commandErrorContains(err, "does not exist", "cannot find device", "no such process", "no such file or directory", "no such table", "no such file")
}

func commandErrorContains(err error, needles ...string) bool {
	if err == nil {
		return false
	}
	var commandErr commandError
	if errors.As(err, &commandErr) && commandErr.name == "resolvectl" {
		for _, needle := range needles {
			if strings.EqualFold(strings.TrimSpace(needle), "no such device") {
				return resolvedCommandFailureIsMissing(err)
			}
		}
		return false
	}
	text := strings.ToLower(err.Error())
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}
