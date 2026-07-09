package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/recovery"
)

const (
	recoveryResolvedInterface      = "podlaz0"
	recoveryResolvedCandidateKind  = "dns-link"
	recoveryResolvedCommandTimeout = 3 * time.Second
)

func newDaemonRecoveryOptions(runtimeDir string) recovery.Options {
	runner := recovery.OSRunner{}
	return recovery.Options{
		RuntimeDir: runtimeDir,
		Scanner: daemonRecoveryScanner{
			base:   recovery.OSScanner{Runner: runner, RuntimeDir: runtimeDir},
			runner: runner,
		},
		Executor: daemonRecoveryExecutor{
			base:   recovery.DaemonCleanupExecutor{Runner: runner, RuntimeDir: runtimeDir},
			runner: runner,
		},
	}
}

type daemonRecoveryScanner struct {
	base   recovery.OSScanner
	runner recovery.CommandRunner
}

func (s daemonRecoveryScanner) Scan(ctx context.Context) recovery.ScanResult {
	result := s.base.Scan(ctx)
	scanStaleResolvedLink(ctx, s.runner, &result)
	return result
}

type daemonRecoveryExecutor struct {
	base   recovery.DaemonCleanupExecutor
	runner recovery.CommandRunner
}

func (e daemonRecoveryExecutor) Cleanup(ctx context.Context, candidate recovery.Candidate) recovery.CleanupResult {
	results := e.CleanupMany(ctx, candidate)
	if len(results) == 0 {
		return recovery.CleanupResult{Candidate: candidate, Status: "skipped", Message: "no cleanup action produced a result"}
	}
	return results[0]
}

func (e daemonRecoveryExecutor) CleanupMany(ctx context.Context, candidate recovery.Candidate) []recovery.CleanupResult {
	if candidate.Kind != recoveryResolvedCandidateKind {
		return e.base.CleanupMany(ctx, candidate)
	}
	return []recovery.CleanupResult{cleanupStaleResolvedLink(ctx, e.runner, candidate)}
}

func scanStaleResolvedLink(ctx context.Context, runner recovery.CommandRunner, result *recovery.ScanResult) {
	runner = recoveryRunner(runner)
	resolved, err := runRecoveryCommand(ctx, runner, "resolvectl", "status", recoveryResolvedInterface, "--no-pager")
	if recoveryCommandMissing(err) {
		result.Warnings = append(result.Warnings, recovery.Warning{Target: "systemd-resolved link " + recoveryResolvedInterface, Message: "resolvectl command is unavailable"})
		return
	}
	if recoveryResourceMissing(resolved) {
		return
	}
	if !recoveryCommandSucceeded(resolved, err) {
		result.Warnings = append(result.Warnings, recovery.Warning{Target: "systemd-resolved link " + recoveryResolvedInterface, Message: recoveryCommandFailureMessage(resolved, err)})
		return
	}

	ipResult, ipErr := runRecoveryCommand(ctx, runner, "ip", "link", "show", "dev", recoveryResolvedInterface)
	if recoveryCommandMissing(ipErr) {
		result.Warnings = append(result.Warnings, recovery.Warning{Target: "systemd-resolved link " + recoveryResolvedInterface, Message: "ip command is unavailable; cannot prove the resolved link is stale"})
		return
	}
	if recoveryResourceMissing(ipResult) {
		result.Candidates = append(result.Candidates, recovery.Candidate{Kind: recoveryResolvedCandidateKind, Description: "systemd-resolved link state", Target: recoveryResolvedInterface})
		return
	}
	if !recoveryCommandSucceeded(ipResult, ipErr) {
		result.Warnings = append(result.Warnings, recovery.Warning{Target: "systemd-resolved link " + recoveryResolvedInterface, Message: "cannot verify interface absence: " + recoveryCommandFailureMessage(ipResult, ipErr)})
	}
}

func cleanupStaleResolvedLink(ctx context.Context, runner recovery.CommandRunner, candidate recovery.Candidate) recovery.CleanupResult {
	if candidate.Target != recoveryResolvedInterface {
		return recovery.CleanupResult{Candidate: candidate, Status: "skipped", Message: "non-podlaz DNS link target"}
	}
	runner = recoveryRunner(runner)
	ipResult, ipErr := runRecoveryCommand(ctx, runner, "ip", "link", "show", "dev", recoveryResolvedInterface)
	if recoveryCommandMissing(ipErr) {
		return recovery.CleanupResult{Candidate: candidate, Status: "skipped", Message: "ip command is unavailable; cannot prove the resolved link is stale"}
	}
	if recoveryCommandSucceeded(ipResult, ipErr) {
		return recovery.CleanupResult{Candidate: candidate, Status: "skipped", Message: "interface still exists; refusing stale DNS cleanup for a live link"}
	}
	if !recoveryResourceMissing(ipResult) {
		return recovery.CleanupResult{Candidate: candidate, Status: "failed", Message: "verify interface absence: " + recoveryCommandFailureMessage(ipResult, ipErr)}
	}

	revert, err := runRecoveryCommand(ctx, runner, "resolvectl", "revert", recoveryResolvedInterface)
	if recoveryCommandMissing(err) {
		return recovery.CleanupResult{Candidate: candidate, Status: "failed", Message: "resolvectl command is unavailable"}
	}
	if !recoveryCommandSucceeded(revert, err) && !recoveryResourceMissing(revert) {
		return recovery.CleanupResult{Candidate: candidate, Status: "failed", Message: "revert systemd-resolved DNS: " + recoveryCommandFailureMessage(revert, err)}
	}

	status, statusErr := runRecoveryCommand(ctx, runner, "resolvectl", "status", recoveryResolvedInterface, "--no-pager")
	if recoveryCommandMissing(statusErr) || recoveryResourceMissing(status) {
		return recovery.CleanupResult{Candidate: candidate, Status: "recovered"}
	}
	if recoveryCommandSucceeded(status, statusErr) {
		return recovery.CleanupResult{Candidate: candidate, Status: "skipped", Message: "systemd-resolved link record persisted after revert; restart systemd-resolved manually"}
	}
	return recovery.CleanupResult{Candidate: candidate, Status: "failed", Message: "verify systemd-resolved cleanup: " + recoveryCommandFailureMessage(status, statusErr)}
}

func recoveryRunner(runner recovery.CommandRunner) recovery.CommandRunner {
	if runner == nil {
		return recovery.OSRunner{}
	}
	return runner
}

func runRecoveryCommand(ctx context.Context, runner recovery.CommandRunner, command string, args ...string) (recovery.CommandResult, error) {
	path, err := runner.LookPath(command)
	if err != nil {
		return recovery.CommandResult{ExitCode: -1}, fmt.Errorf("%s command is unavailable: %w", command, err)
	}
	cmdCtx, cancel := context.WithTimeout(ctx, recoveryResolvedCommandTimeout)
	defer cancel()
	return runner.Run(cmdCtx, path, args...)
}

func recoveryCommandSucceeded(result recovery.CommandResult, err error) bool {
	return err == nil && result.ExitCode == 0
}

func recoveryCommandMissing(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "command is unavailable")
}

func recoveryResourceMissing(result recovery.CommandResult) bool {
	if result.ExitCode == 0 {
		return false
	}
	text := strings.ToLower(result.Stdout + " " + result.Stderr)
	return strings.Contains(text, "does not exist") || strings.Contains(text, "cannot find device") || strings.Contains(text, "no such file or directory") || strings.Contains(text, "no such table")
}

func recoveryCommandFailureMessage(result recovery.CommandResult, err error) string {
	parts := make([]string, 0, 3)
	if result.ExitCode >= 0 {
		parts = append(parts, fmt.Sprintf("exit code %d", result.ExitCode))
	}
	if strings.TrimSpace(result.Stderr) != "" {
		parts = append(parts, "stderr: "+strings.Join(strings.Fields(result.Stderr), " "))
	}
	if err != nil && strings.TrimSpace(result.Stderr) == "" {
		if errors.Is(err, context.DeadlineExceeded) {
			parts = append(parts, "deadline exceeded")
		} else {
			parts = append(parts, err.Error())
		}
	}
	if len(parts) == 0 {
		return "command failed"
	}
	return strings.Join(parts, ", ")
}
