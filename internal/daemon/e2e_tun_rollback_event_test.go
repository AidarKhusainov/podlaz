package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

const testMissingLinkStderr = "Failed to resolve interface \"podlaz0\": No such device\n"

func TestE2EDNSRollbackCaptureRecordsActualProductionDelegateResult(t *testing.T) {
	hookDir := t.TempDir()
	t.Setenv(e2eTunHookGateEnv, "true")
	t.Setenv(e2eTunHookPhaseEnv, e2eTunHookDNSMissingLinkRollbackPhase)
	t.Setenv(e2eTunHookDirEnv, hookDir)

	runner := &productionRollbackResultRunner{}
	executor := newProductionTunPlanExecutor(runner)
	dnsAware, ok := executor.(netexecutor.DNSAwareTunExecutor)
	if !ok {
		t.Fatalf("expected DNS-aware production executor, got %T", executor)
	}
	if err := dnsAware.DNS.Rollback(context.Background(), planner.TunDNSPlan{TargetLink: "podlaz0"}); err != nil {
		t.Fatalf("production DNS rollback rejected captured missing-link result: %v", err)
	}
	if runner.calls != 1 {
		t.Fatalf("production delegate calls = %d, want 1", runner.calls)
	}

	assertE2ECaptureFile(t, hookDir, e2eTunHookDNSRollbackExitCodeFile, "1\n")
	assertE2ECaptureFile(t, hookDir, e2eTunHookDNSRollbackStdoutFile, "")
	assertE2ECaptureFile(t, hookDir, e2eTunHookDNSRollbackStderrFile, testMissingLinkStderr)
	events, err := os.ReadFile(filepath.Join(hookDir, e2eTunHookEventsFile))
	if err != nil {
		t.Fatalf("read E2E event log: %v", err)
	}
	if string(events) != "dns-rollback-started\ndns-rollback-result-captured\n" {
		t.Fatalf("unexpected production rollback event order: %q", events)
	}
}

func TestE2EDNSRollbackCaptureIgnoresApplyTimeRevertWithoutRollbackContext(t *testing.T) {
	hookDir := t.TempDir()
	t.Setenv(e2eTunHookGateEnv, "true")
	t.Setenv(e2eTunHookPhaseEnv, e2eTunHookDNSMissingLinkRollbackPhase)
	t.Setenv(e2eTunHookDirEnv, hookDir)

	runner := e2eDNSRollbackCaptureRunner{delegate: &productionRollbackResultRunner{}}
	result, err := runner.Run(context.Background(), "resolvectl", "revert", "podlaz0")
	if err == nil || result.ExitCode != 1 {
		t.Fatalf("unexpected uncaptured apply-time result: result=%#v err=%v", result, err)
	}
	for _, name := range []string{
		e2eTunHookDNSRollbackCaptureClaimFile,
		e2eTunHookDNSRollbackExitCodeFile,
		e2eTunHookDNSRollbackStdoutFile,
		e2eTunHookDNSRollbackStderrFile,
	} {
		if _, statErr := os.Stat(filepath.Join(hookDir, name)); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("apply-time revert unexpectedly created %s: %v", name, statErr)
		}
	}
}

func TestE2EDNSRollbackCaptureFailureFailsProductionRollbackClosed(t *testing.T) {
	hookDir := t.TempDir()
	t.Setenv(e2eTunHookGateEnv, "true")
	t.Setenv(e2eTunHookPhaseEnv, e2eTunHookDNSMissingLinkRollbackPhase)
	t.Setenv(e2eTunHookDirEnv, hookDir)
	if err := os.WriteFile(filepath.Join(hookDir, e2eTunHookDNSRollbackCaptureClaimFile), []byte("claimed\n"), 0o600); err != nil {
		t.Fatalf("create duplicate capture claim: %v", err)
	}

	executor := newProductionTunPlanExecutor(&productionRollbackResultRunner{})
	dnsAware := executor.(netexecutor.DNSAwareTunExecutor)
	err := dnsAware.DNS.Rollback(context.Background(), planner.TunDNSPlan{TargetLink: "podlaz0"})
	if err == nil || !strings.Contains(err.Error(), "capture production DNS rollback result") {
		t.Fatalf("capture failure did not fail production rollback closed: %v", err)
	}
}

type productionRollbackResultRunner struct {
	calls int
}

func (r *productionRollbackResultRunner) Run(context.Context, string, ...string) (netexecutor.CommandResult, error) {
	r.calls++
	return netexecutor.CommandResult{
		Stderr:    strings.TrimSpace(testMissingLinkStderr),
		RawStderr: testMissingLinkStderr,
		ExitCode:  1,
	}, errors.New("exit status 1")
}

func assertE2ECaptureFile(t *testing.T, dir, name, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", name, data, want)
	}
}
