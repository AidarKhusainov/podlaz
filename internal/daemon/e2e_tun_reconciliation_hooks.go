package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

const (
	e2eTunReconciliationSoftFailureEnv       = "PODLAZ_E2E_TUN_RECONCILIATION_SOFT_FAILURE"
	e2eTunReconciliationSoftFailureTrigger   = "reconciliation-soft-provider.trigger"
	e2eTunReconciliationSoftFailureInjected  = "reconciliation-soft-provider.injected"
	e2eTunReconciliationResolvedUnknownEnv   = "PODLAZ_E2E_TUN_RECONCILIATION_RESOLVED_UNKNOWN"
	e2eTunReconciliationResolvedUnknownTrig  = "reconciliation-resolved-unknown.trigger"
	e2eTunReconciliationResolvedUnknownDone  = "reconciliation-resolved-unknown.injected"
	e2eTunReconciliationOwnedRouteDriftEnv   = "PODLAZ_E2E_TUN_RECONCILIATION_OWNED_ROUTE_DRIFT"
	e2eTunReconciliationOwnedRouteDriftTrig  = "reconciliation-owned-route-drift.trigger"
	e2eTunReconciliationOwnedRouteDriftDone  = "reconciliation-owned-route-drift.injected"
	e2eTunReconciliationRebuildPauseEnv      = "PODLAZ_E2E_TUN_RECONCILIATION_REBUILD_PAUSE"
	e2eTunReconciliationRebuildReady         = "reconciliation-rebuild.ready"
	e2eTunReconciliationRebuildContinue      = "reconciliation-rebuild.continue"
)

func maybeInjectE2ETunReconciliationSoftFailure(probes []tunProbeEvidence) []tunProbeEvidence {
	dir, enabled := e2eTunReconciliationDir(e2eTunReconciliationSoftFailureEnv)
	if !enabled {
		return probes
	}
	trigger := filepath.Join(dir, e2eTunReconciliationSoftFailureTrigger)
	if _, err := os.Stat(trigger); err != nil {
		return probes
	}

	copy := append([]tunProbeEvidence(nil), probes...)
	for i := range copy {
		if copy[i].Provider != "cloudflare" || copy[i].Group != "tls" {
			continue
		}
		copy[i].Success = false
		copy[i].Cause = errors.New("controlled E2E soft provider failure")
		_ = os.Remove(trigger)
		_ = os.WriteFile(filepath.Join(dir, e2eTunReconciliationSoftFailureInjected), []byte("injected\n"), 0o600)
		return copy
	}
	return probes
}

func maybeInjectE2ETunReconciliationResolvedUnknown(evidence tunMandatoryEvidence) tunMandatoryEvidence {
	dir, enabled := e2eTunReconciliationDir(e2eTunReconciliationResolvedUnknownEnv)
	if !enabled {
		return evidence
	}
	trigger := filepath.Join(dir, e2eTunReconciliationResolvedUnknownTrig)
	if _, err := os.Stat(trigger); err != nil {
		return evidence
	}
	evidence.ResolvedDNS = tunLocalProofUnknown
	_ = os.Remove(trigger)
	_ = os.WriteFile(filepath.Join(dir, e2eTunReconciliationResolvedUnknownDone), []byte("injected\n"), 0o600)
	return evidence
}

func maybeInjectE2ETunReconciliationOwnedRouteDrift(ctx context.Context, plan planner.TunPlan) error {
	executor := netexecutor.NewOSExecutor()
	return maybeInjectE2ETunReconciliationOwnedRouteDriftWithExecutor(ctx, plan, executor.Routes)
}

func maybeInjectE2ETunReconciliationOwnedRouteDriftWithExecutor(ctx context.Context, plan planner.TunPlan, executor netexecutor.RouteExecutor) error {
	dir, enabled := e2eTunReconciliationDir(e2eTunReconciliationOwnedRouteDriftEnv)
	if !enabled {
		return nil
	}
	trigger := filepath.Join(dir, e2eTunReconciliationOwnedRouteDriftTrig)
	if _, err := os.Stat(trigger); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect owned-route drift trigger: %w", err)
	}
	if executor == nil {
		return errors.New("owned-route drift hook requires typed route executor")
	}

	var owned []planner.TunRoutePlan
	for _, route := range plan.Routes {
		if route.Family == "ipv4" &&
			route.Destination == planner.IPv4DefaultRoute &&
			route.Table == planner.TunRoutingTable &&
			route.Interface == plan.TunDevice.Name &&
			planner.IsTunAddAction(route.Action) {
			owned = append(owned, route)
		}
	}
	if len(owned) != 1 {
		return fmt.Errorf("owned-route drift hook requires exactly one product default route, found %d", len(owned))
	}
	if err := executor.Rollback(ctx, owned[0]); err != nil {
		return fmt.Errorf("remove exact Podlaz-owned route for controlled drift: %w", err)
	}
	if err := os.Remove(trigger); err != nil {
		return fmt.Errorf("consume owned-route drift trigger: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, e2eTunReconciliationOwnedRouteDriftDone), []byte("injected\n"), 0o600); err != nil {
		return fmt.Errorf("write owned-route drift injected marker: %w", err)
	}
	return nil
}

func maybePauseE2ETunReconciliationRebuild(ctx context.Context) error {
	dir, enabled := e2eTunReconciliationDir(e2eTunReconciliationRebuildPauseEnv)
	if !enabled {
		return nil
	}
	ready := filepath.Join(dir, e2eTunReconciliationRebuildReady)
	if err := os.WriteFile(ready, []byte("ready\n"), 0o600); err != nil {
		return fmt.Errorf("write reconciliation rebuild E2E marker: %w", err)
	}
	continuePath := filepath.Join(dir, e2eTunReconciliationRebuildContinue)
	timer := time.NewTimer(e2eTunHookTimeout())
	defer timer.Stop()
	ticker := time.NewTicker(e2eTunTerminalFailurePollPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("controlled reconciliation rebuild pause timed out")
		case <-ticker.C:
			if _, err := os.Stat(continuePath); err == nil {
				_ = os.Remove(continuePath)
				_ = os.Remove(ready)
				return nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("inspect reconciliation rebuild continue marker: %w", err)
			}
		}
	}
}

func e2eTunReconciliationTriggerPending() bool {
	dir := strings.TrimSpace(os.Getenv(e2eTunTerminalFailureDirEnv))
	if !e2eTunTerminalFailureEnabled() || dir == "" {
		return false
	}
	for _, name := range []string{
		e2eTunReconciliationSoftFailureTrigger,
		e2eTunReconciliationResolvedUnknownTrig,
		e2eTunReconciliationOwnedRouteDriftTrig,
	} {
		if _, err := os.Stat(filepath.Join(filepath.Clean(dir), name)); err == nil {
			return true
		}
	}
	return false
}

func e2eTunReconciliationDir(featureEnv string) (string, bool) {
	if !e2eTunTerminalFailureEnabled() || !envFlagEnabled(featureEnv) {
		return "", false
	}
	dir := strings.TrimSpace(os.Getenv(e2eTunTerminalFailureDirEnv))
	if dir == "" {
		return "", false
	}
	dir = filepath.Clean(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", false
	}
	return dir, true
}

func envFlagEnabled(name string) bool {
	value := strings.TrimSpace(os.Getenv(name))
	return value == "1" || strings.EqualFold(value, "true")
}
