package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type PolkitAuthorizer struct {
	CommandPath          string
	AllowUserInteraction bool
	lookupPath           func(string) (string, error)
	run                  func(context.Context, string, []string) error
}

func (PolkitAuthorizer) RequiresPeerCredentials() bool { return true }

func (a PolkitAuthorizer) Authorize(ctx context.Context, action AuthorizationAction, subject PeerSubject) error {
	if action == "" {
		return fmt.Errorf("%w: missing polkit action", ErrAuthorizationUnavailable)
	}
	if subject.PID <= 0 {
		return fmt.Errorf("%w: missing local peer process for %s", ErrAuthorizationUnavailable, action)
	}
	if subject.StartTime == 0 {
		return fmt.Errorf("%w: missing local peer process start time for %s", ErrAuthorizationUnavailable, action)
	}
	command, err := a.resolveCommand()
	if err != nil {
		return err
	}
	subjectSpec := strconv.Itoa(subject.PID) + "," + strconv.FormatUint(subject.StartTime, 10) + "," + strconv.FormatUint(uint64(subject.UID), 10)
	args := []string{"--action-id", string(action), "--process", subjectSpec}
	if a.AllowUserInteraction {
		args = append(args, "--allow-user-interaction")
	}
	if err := a.runCommand(ctx, command, args); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return fmt.Errorf("%w: polkit denied %s; keep using the non-root podlaz CLI and authenticate through a desktop or TTY polkit agent when available", ErrAuthorizationDenied, action)
		}
		return fmt.Errorf("%w: polkit is unavailable for %s; ensure polkitd is installed and running and that a desktop or TTY polkit authentication agent is available", ErrAuthorizationUnavailable, action)
	}
	return nil
}

func (a PolkitAuthorizer) resolveCommand() (string, error) {
	command := strings.TrimSpace(a.CommandPath)
	if command == "" {
		command = "pkcheck"
	}
	if strings.ContainsRune(command, os.PathSeparator) {
		return command, nil
	}
	lookupPath := exec.LookPath
	if a.lookupPath != nil {
		lookupPath = a.lookupPath
	}
	resolved, err := lookupPath(command)
	if err != nil {
		return "", fmt.Errorf("%w: pkcheck is not installed or not in PATH; install polkit/polkitd", ErrAuthorizationUnavailable)
	}
	return resolved, nil
}

func (a PolkitAuthorizer) runCommand(ctx context.Context, command string, args []string) error {
	if a.run != nil {
		return a.run(ctx, command, args)
	}
	return exec.CommandContext(ctx, command, args...).Run()
}
