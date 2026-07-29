package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

const (
	e2eTunHookDNSRollbackCaptureClaimFile = "dns-rollback.capture-claimed"
	e2eTunHookDNSRollbackExitCodeFile     = "dns-rollback.exit-code"
	e2eTunHookDNSRollbackStdoutFile       = "dns-rollback.stdout"
	e2eTunHookDNSRollbackStderrFile       = "dns-rollback.stderr"
)

type e2eDNSRollbackCaptureContextKey struct{}

func maybeRecordE2EDNSRollback(executor tunPlanExecutor) tunPlanExecutor {
	phase := e2eTunHookPhase()
	switch phase {
	case e2eTunHookDNSApplyPhase, e2eTunHookDNSMissingLinkRollbackPhase:
		dnsAware, ok := executor.(netexecutor.DNSAwareTunExecutor)
		if !ok || dnsAware.DNS == nil {
			return executor
		}
		if phase == e2eTunHookDNSMissingLinkRollbackPhase {
			hook, ok := dnsAware.DNS.(e2eHookDNSMissingLinkRollbackExecutor)
			if !ok {
				return e2eHookConfigurationErrorExecutor{err: fmt.Errorf("E2E missing-link capture requires rollback hook executor, got %T", dnsAware.DNS)}
			}
			resolved, ok := hook.delegate.(netexecutor.ResolvedDNSExecutor)
			if !ok {
				return e2eHookConfigurationErrorExecutor{err: fmt.Errorf("E2E missing-link capture requires ResolvedDNSExecutor, got %T", hook.delegate)}
			}
			resolved.Runner = e2eDNSRollbackCaptureRunner{delegate: resolved.Runner}
			hook.delegate = resolved
			dnsAware.DNS = hook
		}
		dnsAware.DNS = e2eDNSRollbackEventExecutor{delegate: dnsAware.DNS}
		return dnsAware
	default:
		return executor
	}
}

type e2eDNSRollbackEventExecutor struct {
	delegate netexecutor.DNSExecutor
}

func (e e2eDNSRollbackEventExecutor) Apply(ctx context.Context, plan planner.TunDNSPlan) (netexecutor.Step, error) {
	return e.delegate.Apply(ctx, plan)
}

func (e e2eDNSRollbackEventExecutor) Verify(ctx context.Context, plan planner.TunDNSPlan) error {
	return e.delegate.Verify(ctx, plan)
}

func (e e2eDNSRollbackEventExecutor) Rollback(ctx context.Context, plan planner.TunDNSPlan) error {
	recordE2ETunHookEvent("dns-rollback-started")
	ctx = context.WithValue(ctx, e2eDNSRollbackCaptureContextKey{}, plan.TargetLink)
	return e.delegate.Rollback(ctx, plan)
}

type e2eDNSRollbackCaptureRunner struct {
	delegate netexecutor.CommandRunner
}

func (r e2eDNSRollbackCaptureRunner) Run(ctx context.Context, name string, args ...string) (netexecutor.CommandResult, error) {
	delegate := r.delegate
	if delegate == nil {
		delegate = netexecutor.OSRunner{}
	}
	result, runErr := delegate.Run(ctx, name, args...)
	target, _ := ctx.Value(e2eDNSRollbackCaptureContextKey{}).(string)
	if target == "" || name != "resolvectl" || !equalStringSlice(args, []string{"revert", target}) {
		return result, runErr
	}
	if err := captureE2EDNSRollbackResult(result); err != nil {
		// The production missing-link matcher must not convert instrumentation
		// failure into idempotent success. Return an unmistakable launch-like
		// result while preserving the capture error as the command cause.
		return netexecutor.CommandResult{ExitCode: -1}, fmt.Errorf("capture production DNS rollback result: %w", err)
	}
	return result, runErr
}

func captureE2EDNSRollbackResult(result netexecutor.CommandResult) (captureErr error) {
	dir := e2eTunHookDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create E2E TUN hook directory: %w", err)
	}
	claimPath := filepath.Join(dir, e2eTunHookDNSRollbackCaptureClaimFile)
	claim, err := os.OpenFile(claimPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("claim DNS rollback capture: %w", err)
	}
	if err := claim.Close(); err != nil {
		_ = os.Remove(claimPath)
		return fmt.Errorf("close DNS rollback capture claim: %w", err)
	}

	paths := []string{
		filepath.Join(dir, e2eTunHookDNSRollbackExitCodeFile),
		filepath.Join(dir, e2eTunHookDNSRollbackStdoutFile),
		filepath.Join(dir, e2eTunHookDNSRollbackStderrFile),
	}
	defer func() {
		if captureErr == nil {
			return
		}
		for _, path := range paths {
			_ = os.Remove(path)
		}
		_ = os.Remove(claimPath)
	}()

	payloads := [][]byte{
		[]byte(strconv.Itoa(result.ExitCode) + "\n"),
		[]byte(result.RawStdout),
		[]byte(result.RawStderr),
	}
	for i, path := range paths {
		if err := writeE2EPrivateFileAtomically(path, payloads[i]); err != nil {
			return err
		}
	}
	if err := appendE2ETunHookEventStrict("dns-rollback-result-captured"); err != nil {
		return err
	}
	return nil
}

func writeE2EPrivateFileAtomically(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".dns-rollback-*.tmp")
	if err != nil {
		return fmt.Errorf("create private E2E capture temp file: %w", err)
	}
	tmpPath := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set private E2E capture permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write private E2E capture: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync private E2E capture: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close private E2E capture: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace private E2E capture: %w", err)
	}
	removeTemp = false
	return nil
}

func appendE2ETunHookEventStrict(event string) error {
	path := filepath.Join(e2eTunHookDir(), e2eTunHookEventsFile)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open E2E event log: %w", err)
	}
	if _, err := fmt.Fprintln(file, event); err != nil {
		_ = file.Close()
		return fmt.Errorf("append E2E event: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync E2E event log: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close E2E event log: %w", err)
	}
	return nil
}
