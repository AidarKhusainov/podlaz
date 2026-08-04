package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

const (
	e2eTunHookGateEnv           = "PODLAZ_E2E_TUN_HOOKS"
	e2eTunHookPhaseEnv          = "PODLAZ_E2E_TUN_HOOK_PHASE"
	e2eTunHookDirEnv            = "PODLAZ_E2E_TUN_HOOK_DIR"
	e2eTunHookTimeoutSecondsEnv = "PODLAZ_E2E_TUN_HOOK_TIMEOUT_SECONDS"

	e2eTunHookTunAddressApplyPhase        = "tun-address-apply"
	e2eTunHookRouteApplyPhase             = "route-apply"
	e2eTunHookDNSApplyPhase               = "dns-apply"
	e2eTunHookNetworkVerifyPhase          = "network-verify"
	e2eTunHookDNSInactiveScopePhase       = "dns-inactive-scope"
	e2eTunHookDNSMissingLinkRollbackPhase = "dns-missing-link-rollback"
	e2eTunHookBeforeCommitPausePhase      = "before-commit-pause"
	e2eTunHookEventsFile                  = "events.log"
	e2eTunHookDNSMissingLinkReadyFile     = "dns-missing-link.ready"
	e2eTunHookDNSMissingLinkContinueFile  = "dns-missing-link.continue"
)

func e2eTunHooksEnabled() bool {
	value := strings.TrimSpace(os.Getenv(e2eTunHookGateEnv))
	return value == "1" || strings.EqualFold(value, "true")
}

func e2eTunHookPhase() string {
	if !e2eTunHooksEnabled() {
		return ""
	}
	return strings.TrimSpace(os.Getenv(e2eTunHookPhaseEnv))
}

func validateE2ETunHookConfig() error {
	if !e2eTunHooksEnabled() {
		return nil
	}
	switch e2eTunHookPhase() {
	case e2eTunHookTunAddressApplyPhase,
		e2eTunHookRouteApplyPhase,
		e2eTunHookDNSApplyPhase,
		e2eTunHookNetworkVerifyPhase,
		e2eTunHookDNSInactiveScopePhase,
		e2eTunHookDNSMissingLinkRollbackPhase,
		e2eTunHookBeforeCommitPausePhase:
		return nil
	case "":
		return fmt.Errorf("%s is enabled but %s is empty", e2eTunHookGateEnv, e2eTunHookPhaseEnv)
	default:
		return fmt.Errorf("unsupported %s=%q", e2eTunHookPhaseEnv, e2eTunHookPhase())
	}
}

func maybeWrapE2ETunHookExecutor(executor netexecutor.DNSAwareTunExecutor) tunPlanExecutor {
	switch e2eTunHookPhase() {
	case e2eTunHookTunAddressApplyPhase:
		executor.Base.TunAddress = e2eHookTunAddressExecutor{delegate: executor.Base.TunAddress}
	case e2eTunHookRouteApplyPhase:
		executor.Base.Routes = e2eHookRouteExecutor{delegate: executor.Base.Routes}
	case e2eTunHookDNSApplyPhase:
		executor.DNS = e2eHookDNSExecutor{delegate: executor.DNS}
	case e2eTunHookNetworkVerifyPhase:
		return e2eHookNetworkVerifyExecutor{delegate: executor}
	case e2eTunHookDNSInactiveScopePhase:
		resolved, ok := executor.DNS.(netexecutor.ResolvedDNSExecutor)
		if !ok {
			return e2eHookConfigurationErrorExecutor{err: fmt.Errorf("E2E inactive-scope hook requires ResolvedDNSExecutor, got %T", executor.DNS)}
		}
		resolved.Runner = e2eInactiveScopeCommandRunner{delegate: resolved.Runner}
		executor.DNS = resolved
	case e2eTunHookDNSMissingLinkRollbackPhase:
		executor.DNS = e2eHookDNSMissingLinkRollbackExecutor{delegate: executor.DNS}
	}
	return executor
}

func maybePauseForE2ETunHook(ctx context.Context, transactionID string) error {
	if e2eTunHookPhase() != e2eTunHookBeforeCommitPausePhase {
		return nil
	}
	dir := e2eTunHookDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create E2E TUN hook directory: %w", err)
	}
	marker := filepath.Join(dir, "before-commit-pause.ready")
	body := fmt.Sprintf("transaction_id=%s\nphase=%s\n", transactionID, e2eTunHookBeforeCommitPausePhase)
	if err := os.WriteFile(marker, []byte(body), 0o600); err != nil {
		return fmt.Errorf("write E2E TUN hook marker: %w", err)
	}
	timer := time.NewTimer(e2eTunHookTimeout())
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("E2E TUN hook %s timed out", e2eTunHookBeforeCommitPausePhase)
	}
}

func e2eTunHookDir() string {
	dir := strings.TrimSpace(os.Getenv(e2eTunHookDirEnv))
	if dir == "" {
		return filepath.Join(os.TempDir(), "podlaz-e2e-tun-hooks")
	}
	return filepath.Clean(dir)
}

func recordE2ETunHookEvent(event string) {
	if !e2eTunHooksEnabled() {
		return
	}
	event = strings.TrimSpace(event)
	if event == "" || len(event) > 128 || strings.ContainsAny(event, "\r\n") {
		return
	}
	dir := e2eTunHookDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	path := filepath.Join(dir, e2eTunHookEventsFile)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintln(file, event)
	_ = file.Close()
}

func e2eTunHookTimeout() time.Duration {
	value := strings.TrimSpace(os.Getenv(e2eTunHookTimeoutSecondsEnv))
	if value == "" {
		return 60 * time.Second
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		return 60 * time.Second
	}
	return time.Duration(seconds) * time.Second
}

type e2eHookTunAddressExecutor struct {
	delegate netexecutor.TunAddressExecutor
}

func (e e2eHookTunAddressExecutor) Bind(ctx context.Context, plan planner.TunAddressPlan, proof netexecutor.TunLinkCreationProof) (planner.TunAddressPlan, error) {
	if e.delegate == nil {
		return plan, errors.New("missing TUN address executor")
	}
	return e.delegate.Bind(ctx, plan, proof)
}

func (e e2eHookTunAddressExecutor) Apply(ctx context.Context, plan planner.TunAddressPlan) (netexecutor.Step, error) {
	if e.delegate == nil {
		return netexecutor.Step{}, errors.New("missing TUN address executor")
	}
	step, err := e.delegate.Apply(ctx, plan)
	if err != nil {
		return step, err
	}
	recordE2ETunHookEvent("tun-address-apply-injected")
	return step, fmt.Errorf("%w: E2E hook failed after the daemon-owned TUN address was applied", netexecutor.ErrTunAddressApply)
}

func (e e2eHookTunAddressExecutor) Verify(ctx context.Context, plan planner.TunAddressPlan) error {
	if e.delegate == nil {
		return errors.New("missing TUN address executor")
	}
	return e.delegate.Verify(ctx, plan)
}

func (e e2eHookTunAddressExecutor) Rollback(ctx context.Context, plan planner.TunAddressPlan) error {
	if e.delegate == nil {
		return errors.New("missing TUN address executor")
	}
	return e.delegate.Rollback(ctx, plan)
}

type e2eHookRouteExecutor struct {
	delegate netexecutor.RouteExecutor
}

func (e e2eHookRouteExecutor) Add(context.Context, planner.TunRoutePlan) (netexecutor.Step, error) {
	if e.delegate == nil {
		return netexecutor.Step{}, errors.New("missing route executor")
	}
	recordE2ETunHookEvent("route-apply-injected")
	return netexecutor.Step{}, errors.New("E2E hook: route apply failed before adding podlaz-owned route")
}

func (e e2eHookRouteExecutor) Verify(ctx context.Context, plan planner.TunRoutePlan) error {
	if e.delegate == nil {
		return errors.New("missing route executor")
	}
	return e.delegate.Verify(ctx, plan)
}

func (e e2eHookRouteExecutor) Rollback(ctx context.Context, plan planner.TunRoutePlan) error {
	if e.delegate == nil {
		return errors.New("missing route executor")
	}
	return e.delegate.Rollback(ctx, plan)
}

type e2eHookDNSExecutor struct {
	delegate netexecutor.DNSExecutor
}

func (e e2eHookDNSExecutor) Apply(ctx context.Context, plan planner.TunDNSPlan) (netexecutor.Step, error) {
	if e.delegate == nil {
		return netexecutor.Step{}, errors.New("missing DNS executor")
	}
	step, err := e.delegate.Apply(ctx, plan)
	if err != nil {
		return step, err
	}
	recordE2ETunHookEvent("dns-apply-injected")
	return step, errors.New("E2E hook: DNS apply failed after podlaz-owned per-link DNS was applied")
}

func (e e2eHookDNSExecutor) Verify(ctx context.Context, plan planner.TunDNSPlan) error {
	if e.delegate == nil {
		return errors.New("missing DNS executor")
	}
	return e.delegate.Verify(ctx, plan)
}

func (e e2eHookDNSExecutor) Rollback(ctx context.Context, plan planner.TunDNSPlan) error {
	if e.delegate == nil {
		return errors.New("missing DNS executor")
	}
	return e.delegate.Rollback(ctx, plan)
}

type e2eHookDNSMissingLinkRollbackExecutor struct {
	delegate netexecutor.DNSExecutor
}

func (e e2eHookDNSMissingLinkRollbackExecutor) Apply(ctx context.Context, plan planner.TunDNSPlan) (netexecutor.Step, error) {
	if e.delegate == nil {
		return netexecutor.Step{}, errors.New("missing DNS executor")
	}
	step, err := e.delegate.Apply(ctx, plan)
	if err != nil {
		return step, err
	}
	if err := pauseForE2EDNSMissingLinkRollback(ctx); err != nil {
		return step, err
	}
	return step, nil
}

func (e e2eHookDNSMissingLinkRollbackExecutor) Verify(ctx context.Context, plan planner.TunDNSPlan) error {
	if e.delegate == nil {
		return errors.New("missing DNS executor")
	}
	return e.delegate.Verify(ctx, plan)
}

func (e e2eHookDNSMissingLinkRollbackExecutor) Rollback(ctx context.Context, plan planner.TunDNSPlan) error {
	if e.delegate == nil {
		return errors.New("missing DNS executor")
	}
	return e.delegate.Rollback(ctx, plan)
}

func pauseForE2EDNSMissingLinkRollback(ctx context.Context) error {
	dir := e2eTunHookDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create E2E TUN hook directory: %w", err)
	}
	ready := filepath.Join(dir, e2eTunHookDNSMissingLinkReadyFile)
	if err := os.WriteFile(ready, []byte("phase="+e2eTunHookDNSMissingLinkRollbackPhase+"\n"), 0o600); err != nil {
		return fmt.Errorf("write E2E missing-link ready marker: %w", err)
	}
	recordE2ETunHookEvent("dns-missing-link-ready")

	continuePath := filepath.Join(dir, e2eTunHookDNSMissingLinkContinueFile)
	timer := time.NewTimer(e2eTunHookTimeout())
	defer timer.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("E2E TUN hook %s timed out", e2eTunHookDNSMissingLinkRollbackPhase)
		case <-ticker.C:
			if _, err := os.Stat(continuePath); err == nil {
				recordE2ETunHookEvent("dns-missing-link-released")
				return nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("inspect E2E missing-link continue marker: %w", err)
			}
		}
	}
}

type e2eHookNetworkVerifyExecutor struct {
	delegate tunPlanExecutor
}

func (e e2eHookNetworkVerifyExecutor) Apply(ctx context.Context, plan planner.TunPlan) ([]netexecutor.Step, error) {
	return e.delegate.Apply(ctx, plan)
}

func (e e2eHookNetworkVerifyExecutor) ApplyWithStepSink(ctx context.Context, plan planner.TunPlan, sink netexecutor.AppliedStepSink) ([]netexecutor.Step, error) {
	incremental, ok := e.delegate.(incrementalTunPlanExecutor)
	if !ok {
		return nil, errors.New("E2E network verification delegate does not support incremental ownership persistence")
	}
	return incremental.ApplyWithStepSink(ctx, plan, sink)
}

func (e e2eHookNetworkVerifyExecutor) Verify(ctx context.Context, plan planner.TunPlan) error {
	if err := e.delegate.Verify(ctx, plan); err != nil {
		return err
	}
	recordE2ETunHookEvent("network-verify-injected")
	return errors.New("E2E hook: network verification failed after production verification succeeded")
}

func (e e2eHookNetworkVerifyExecutor) Rollback(ctx context.Context, plan planner.TunPlan) error {
	return e.delegate.Rollback(ctx, plan)
}

func (e e2eHookNetworkVerifyExecutor) BindTunAddress(ctx context.Context, plan planner.TunPlan, proof netexecutor.TunLinkCreationProof) (planner.TunPlan, error) {
	binder, ok := e.delegate.(tunAddressIdentityBinder)
	if !ok {
		return plan, errors.New("E2E network verification delegate cannot bind TUN address identity")
	}
	return binder.BindTunAddress(ctx, plan, proof)
}

type e2eHookConfigurationErrorExecutor struct {
	err error
}

func (e e2eHookConfigurationErrorExecutor) Apply(context.Context, planner.TunPlan) ([]netexecutor.Step, error) {
	return nil, e.err
}

func (e e2eHookConfigurationErrorExecutor) Verify(context.Context, planner.TunPlan) error {
	return e.err
}

func (e e2eHookConfigurationErrorExecutor) Rollback(context.Context, planner.TunPlan) error {
	return e.err
}

func (e e2eHookConfigurationErrorExecutor) BindTunAddress(context.Context, planner.TunPlan, netexecutor.TunLinkCreationProof) (planner.TunPlan, error) {
	return planner.TunPlan{}, e.err
}

type e2eInactiveScopeCommandRunner struct {
	delegate netexecutor.CommandRunner
}

func (r e2eInactiveScopeCommandRunner) Run(ctx context.Context, name string, args ...string) (netexecutor.CommandResult, error) {
	delegate := r.delegate
	if delegate == nil {
		delegate = netexecutor.OSRunner{}
	}
	result, err := delegate.Run(ctx, name, args...)
	if err != nil || name != "resolvectl" || !equalStringSlice(args, []string{"status", "--no-pager"}) {
		return result, err
	}
	updated, replaced := replaceResolvedCurrentScopes(result.Stdout, "podlaz0", "none")
	if replaced {
		result.Stdout = updated
		recordE2ETunHookEvent("resolved-current-scopes-none")
	}
	return result, err
}

func replaceResolvedCurrentScopes(output, linkName, value string) (string, bool) {
	lines := strings.Split(output, "\n")
	inTarget := false
	replaced := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Link ") {
			inTarget = strings.Contains(trimmed, "("+linkName+")")
			continue
		}
		if !inTarget || !strings.HasPrefix(trimmed, "Current Scopes:") {
			continue
		}
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		lines[i] = indent + "Current Scopes: " + value
		replaced = true
		break
	}
	return strings.Join(lines, "\n"), replaced
}

func equalStringSlice(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
