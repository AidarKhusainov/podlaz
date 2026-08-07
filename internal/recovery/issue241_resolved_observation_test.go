package recovery

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanWithOptionsReportsResolvedInspectionUnknownWhenResolvectlUnavailable(t *testing.T) {
	runner := fakeMissingResourcesRunner()
	delete(runner.paths, "resolvectl")
	delete(runner.commands, "resolvectl status podlaz0 --no-pager")

	plan := PlanWithOptions(context.Background(), Options{
		RuntimeDir: filepath.Join(t.TempDir(), "podlaz"),
		Runner:     runner,
	})

	assertNoCandidateKind(t, plan, managedDNSCandidateKind)
	if len(plan.Warnings) != 1 {
		t.Fatalf("missing resolvectl must produce exactly one inspection warning, got %#v", plan.Warnings)
	}
	warning := plan.Warnings[0]
	if warning.Target != managedDNSTarget || !strings.Contains(warning.Message, "resolvectl command is unavailable") {
		t.Fatalf("unexpected resolved inspection warning: %#v", warning)
	}
	if strings.Contains(plan.String(), "No podlaz-owned recovery candidates found.") {
		t.Fatalf("unknown resolved state must not be published as a clean host: %q", plan.String())
	}
}

func TestPlanWithOptionsTreatsEmptyResolvedRecordAsNoActionableState(t *testing.T) {
	runner := newResolvedRecoveryRunner(
		[]resolvedRecoveryCommand{missingPodlazLink(), missingPodlazLink()},
		[]resolvedRecoveryCommand{emptyResolvedLinkRecord()},
	)

	plan := PlanWithOptions(context.Background(), Options{
		Runner:     runner,
		RuntimeDir: filepath.Join(t.TempDir(), "podlaz"),
	})

	assertNoCandidateKind(t, plan, managedDNSCandidateKind)
	if len(plan.Warnings) != 0 {
		t.Fatalf("an empty transient resolved record must be clean, got warnings %#v", plan.Warnings)
	}
	if !strings.Contains(plan.String(), "No podlaz-owned recovery candidates found.") {
		t.Fatalf("empty resolved record must not create a phantom recovery candidate: %q", plan.String())
	}
}

func TestResolvedCleanupTreatsEmptyPostRevertRecordAsRecovered(t *testing.T) {
	runner := newResolvedRecoveryRunner(
		[]resolvedRecoveryCommand{missingPodlazLink()},
		[]resolvedRecoveryCommand{emptyResolvedLinkRecord()},
	)
	runner.commands["resolvectl revert podlaz0"] = []resolvedRecoveryCommand{{}}

	result := (OSCleanupExecutor{Runner: runner}).cleanupManagedResolvedLink(context.Background(), Candidate{
		Kind:        managedDNSCandidateKind,
		Description: managedDNSDescription,
		Target:      managedInterface,
	})

	if result.Status != "recovered" {
		t.Fatalf("empty post-revert resolved record must converge as recovered, got %#v", result)
	}
}

func TestResolvedRecoveryDoesNotRediscoverEmptyPostRevertRecord(t *testing.T) {
	runner := newResolvedRecoveryRunner(
		[]resolvedRecoveryCommand{missingPodlazLink(), missingPodlazLink(), missingPodlazLink(), missingPodlazLink()},
		[]resolvedRecoveryCommand{resolvedLinkExists(), emptyResolvedLinkRecord(), emptyResolvedLinkRecord()},
	)
	runner.commands["resolvectl revert podlaz0"] = []resolvedRecoveryCommand{{}}
	runner.commands["nft list table inet podlaz"] = []resolvedRecoveryCommand{missingNFTTable(), missingNFTTable()}
	runtimeDir := filepath.Join(t.TempDir(), "podlaz")

	executed := ExecuteWithOptions(context.Background(), Options{
		Runner:     runner,
		RuntimeDir: runtimeDir,
		Executor:   DaemonCleanupExecutor{Runner: runner, RuntimeDir: runtimeDir},
	})
	if executed.HasFailures() || executed.HasIncompleteCleanup() {
		t.Fatalf("expected resolved recovery to converge, got %#v", executed)
	}

	postMutation := PlanWithOptions(context.Background(), Options{Runner: runner, RuntimeDir: runtimeDir})
	assertNoCandidateKind(t, postMutation, managedDNSCandidateKind)
	if len(postMutation.Candidates) != 0 || len(postMutation.Warnings) != 0 {
		t.Fatalf("post-recovery authoritative scan rediscovered stale state: %#v", postMutation)
	}
}

func TestObserveResolvedLinkFailsClosedForOperationalAndAmbiguousResults(t *testing.T) {
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	exactMissing := CommandResult{
		RawStderr: resolvedMissingDeviceStderr + "\n",
		Stderr:    resolvedMissingDeviceStderr,
		ExitCode:  1,
	}

	tests := []struct {
		name   string
		ctx    context.Context
		result CommandResult
		err    error
	}{
		{name: "permission denied", ctx: context.Background(), result: CommandResult{RawStderr: "Access denied\n", Stderr: "Access denied", ExitCode: 1}, err: resolvedTestExitError{code: 1}},
		{name: "cancelled exact missing", ctx: cancelledCtx, result: exactMissing, err: resolvedTestExitError{code: 1}},
		{name: "launch error", ctx: context.Background(), result: CommandResult{ExitCode: -1}, err: errors.New("fork/exec resolvectl: executable file not found")},
		{name: "signal termination", ctx: context.Background(), result: CommandResult{ExitCode: -1}, err: errors.New("signal: killed")},
		{name: "unexpected exit code", ctx: context.Background(), result: CommandResult{RawStderr: "unexpected\n", Stderr: "unexpected", ExitCode: 2}, err: resolvedTestExitError{code: 2}},
		{name: "malformed success", ctx: context.Background(), result: CommandResult{Stdout: "unexpected successful output"}},
		{name: "duplicate target sections", ctx: context.Background(), result: CommandResult{Stdout: resolvedLinkExists().stdout + "\n" + resolvedLinkExists().stdout}},
		{name: "concrete non-podlaz dns", ctx: context.Background(), result: CommandResult{Stdout: `Link 7 (podlaz0)
       DNS Servers: 203.0.113.53`}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := observeResolvedLink(tt.ctx, tt.result, tt.err); got != resolvedLinkUnknown {
				t.Fatalf("expected fail-closed unknown observation, got %v", got)
			}
		})
	}
}

func emptyResolvedLinkRecord() resolvedRecoveryCommand {
	return resolvedRecoveryCommand{stdout: `Link 7 (podlaz0)
    Current Scopes: none
         Protocols: -DefaultRoute +LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported`}
}
