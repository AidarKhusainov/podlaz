package recovery

import (
	"context"
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

func emptyResolvedLinkRecord() resolvedRecoveryCommand {
	return resolvedRecoveryCommand{stdout: `Link 7 (podlaz0)
    Current Scopes: none
         Protocols: -DefaultRoute +LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported`}
}
