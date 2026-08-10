package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

const defaultTunRevalidationTimeout = 45 * time.Second

type productionTunRevalidationBackend struct {
	manager *XrayManager
	timeout time.Duration
}

func newProductionTunRevalidationRuntime(manager *XrayManager) *tunRevalidationRuntime {
	backend := &productionTunRevalidationBackend{manager: manager, timeout: defaultTunRevalidationTimeout}
	return newTunRevalidationRuntime(backend.inspect, backend.verify)
}

func (b *productionTunRevalidationBackend) inspect(ctx context.Context) (tunRevalidationObservation, error) {
	if b == nil || b.manager == nil {
		return tunRevalidationObservation{}, newTunRevalidationObservationError(api.TunHealthOwnershipInvalid, errors.New("missing TUN lifecycle manager"))
	}
	state, cmd := b.manager.activeTunRuntimeIdentity()
	if state.Connection != "active" || state.Mode != planner.ModeTun || strings.TrimSpace(state.TransactionID) == "" {
		return tunRevalidationObservation{}, newTunRevalidationObservationError(api.TunHealthOwnershipInvalid, errors.New("active committed TUN runtime identity is unavailable"))
	}
	store := txstate.TransactionStore{RuntimeDir: b.manager.runtimeDir()}
	tx, _, err := store.Load(state.TransactionID)
	if err != nil {
		return tunRevalidationObservation{}, newTunRevalidationObservationError(api.TunHealthOwnershipInvalid, fmt.Errorf("load active TUN transaction: %w", err))
	}
	if err := verifyCommittedTunRuntimeIdentity(state, cmd, tx); err != nil {
		return tunRevalidationObservation{}, newTunRevalidationObservationError(api.TunHealthOwnershipInvalid, err)
	}
	server, err := tunRevalidationServerAddress(tx)
	if err != nil {
		return tunRevalidationObservation{}, newTunRevalidationObservationError(api.TunHealthOwnershipInvalid, err)
	}
	plan, err := tunRevalidationPlanFromTransaction(tx)
	if err != nil {
		return tunRevalidationObservation{}, newTunRevalidationObservationError(api.TunHealthOwnershipInvalid, err)
	}
	snapshot := b.manager.collectTunSnapshot(ctx, netsnapshot.Options{Server: server})
	fingerprint, err := deriveTunUplinkFingerprint(snapshot, operatingSystemInterfaceIndex)
	if err != nil {
		return tunRevalidationObservation{}, newTunRevalidationObservationError(api.TunHealthUplinkFingerprintUnavailable, err)
	}
	plan.Snapshot = snapshot
	return tunRevalidationObservation{fingerprint: fingerprint, plan: plan}, nil
}

func (b *productionTunRevalidationBackend) verify(ctx context.Context, observation tunRevalidationObservation) error {
	if b == nil || b.manager == nil {
		return newTunRevalidationVerificationError(api.TunHealthOwnershipInvalid, errors.New("missing TUN lifecycle manager"))
	}
	timeout := b.timeout
	if timeout <= 0 {
		timeout = defaultTunRevalidationTimeout
	}
	verifyCtx := ctx
	cancel := func() {}
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > timeout {
		verifyCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	if err := b.manager.tunPlanExecutor().Verify(verifyCtx, observation.plan); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return newTunRevalidationVerificationError(api.TunHealthOwnedStateInvalid, fmt.Errorf("verify committed TUN owned state: %w", err))
	}
	if err := verifyTunConnectivity(verifyCtx, observation.plan, tunCoreRuntimePlan{}); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return newTunRevalidationVerificationError(api.TunHealthConnectivityFailed, fmt.Errorf("verify committed TUN connectivity: %w", err))
	}
	return nil
}

func (m *XrayManager) activeTunRuntimeIdentity() (xrayState, *tunRuntimeProcessIdentity) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state
	var runtimeProcess *tunRuntimeProcessIdentity
	if m.cmd != nil && m.cmd.Process != nil {
		runtimeProcess = &tunRuntimeProcessIdentity{PID: m.cmd.Process.Pid}
	}
	return state, runtimeProcess
}

// tunRuntimeProcessIdentity is the minimum supervised-process evidence needed
// for durable ownership comparison. It intentionally exposes no os.Process
// mutation API.
type tunRuntimeProcessIdentity struct {
	PID int
}

func verifyCommittedTunRuntimeIdentity(state xrayState, process *tunRuntimeProcessIdentity, tx txstate.Transaction) error {
	if tx.State != txstate.TransactionCommitted {
		return fmt.Errorf("active TUN transaction state is %s, want committed", tx.State)
	}
	if tx.Mode != planner.ModeTun || state.Mode != planner.ModeTun {
		return errors.New("active transaction is not TUN mode")
	}
	if tx.ID != state.TransactionID || tx.ProfileID != state.ProfileID {
		return errors.New("active runtime identity does not match persisted transaction")
	}
	if process == nil || process.PID <= 0 {
		return errors.New("active TUN runtime has no supervised Xray process identity")
	}
	if tx.DesiredPlan.Core.Owner != txstate.TransactionOwner || tx.DesiredPlan.Core.ProcessLabel != "xray" || tx.DesiredPlan.Core.RuntimeConfigPath != state.RuntimeConfigPath {
		return errors.New("persisted Xray desired identity does not match active runtime")
	}
	matches := 0
	for _, child := range tx.Rollback.ChildProcesses {
		if child.Owner != txstate.TransactionOwner || child.Label != "xray" {
			continue
		}
		if child.PID == process.PID && child.ConfigRef == state.RuntimeConfigPath {
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("active Xray rollback identity matches=%d, want 1", matches)
	}
	return nil
}

func tunRevalidationServerAddress(tx txstate.Transaction) (string, error) {
	candidates := make(map[string]struct{})
	for _, route := range tx.Rollback.Routes {
		addTunServerBypassCandidate(candidates, route.Table, route.CIDR, route.Dev)
	}
	for _, route := range tx.DesiredPlan.Routes {
		addTunServerBypassCandidate(candidates, route.Table, route.CIDR, route.Dev)
	}
	if len(candidates) != 1 {
		return "", fmt.Errorf("persisted server bypass destination cardinality=%d, want 1", len(candidates))
	}
	for server := range candidates {
		return server, nil
	}
	return "", errors.New("persisted server bypass destination is unavailable")
}

func addTunServerBypassCandidate(candidates map[string]struct{}, table, cidr, device string) {
	if !strings.EqualFold(strings.TrimSpace(table), "main") || strings.TrimSpace(device) == "" || strings.TrimSpace(device) == netsnapshot.DefaultTunName {
		return
	}
	prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
	if err != nil || !prefix.IsValid() || !prefix.Addr().Is4() || prefix.Bits() != 32 {
		return
	}
	candidates[prefix.Addr().String()] = struct{}{}
}

func operatingSystemInterfaceIndex(name string) (int, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return 0, err
	}
	if iface.Index <= 0 {
		return 0, fmt.Errorf("interface %s has invalid index %d", name, iface.Index)
	}
	return iface.Index, nil
}
