package daemon

import (
	"context"
	"errors"
	"fmt"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func newProductionTunEvidenceRevalidationRuntime(manager *XrayManager) *tunEvidenceRevalidationRuntime {
	backend := &productionTunRevalidationBackend{
		manager:       manager,
		timeout:       defaultTunRevalidationTimeout,
		networkClient: newTunRevalidationNetworkClient(),
	}
	return newTunEvidenceRevalidationRuntime(backend.observeReconciliation, newTunReconciliationSupervisor(nil))
}

func (b *productionTunRevalidationBackend) observeReconciliation(ctx context.Context) (tunReconciliationRound, error) {
	round := tunReconciliationRound{}
	if b == nil || b.manager == nil {
		round.OwnershipBlocked = true
		round.Evidence.Mandatory.SessionOwnership = tunLocalProofViolated
		return round, errors.New("missing TUN lifecycle manager")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	store := newNetworkSessionStateStore(b.manager.runtimeDir(), nil)
	session, exists, err := store.Load()
	if err != nil {
		round.OwnershipBlocked = true
		return round, fmt.Errorf("load Network Session for TUN reconciliation: %w", err)
	}
	if !exists {
		round.OwnershipBlocked = true
		round.Evidence.Mandatory.SessionOwnership = tunLocalProofViolated
		return round, errors.New("durable Network Session authority is unavailable")
	}
	round.NetworkSessionID = session.SessionID
	managerState, process := b.manager.activeTunRuntimeIdentity()
	source, err := loadProtectedTunReplacementSource(b.manager.runtimeDir(), managerState, process, session)
	if err != nil {
		round.OwnershipBlocked = true
		round.Evidence.Mandatory.SessionOwnership = tunLocalProofViolated
		return round, fmt.Errorf("prove protected TUN reconciliation source: %w", err)
	}
	round.Evidence.Mandatory.SessionOwnership = tunLocalProofProven
	round.TransactionID = source.Transaction.ID

	server, err := tunRevalidationServerAddress(source.Transaction)
	if err != nil {
		round.OwnershipBlocked = true
		return round, err
	}
	plan, err := tunRevalidationPlanFromTransaction(source.Transaction)
	if err != nil {
		round.OwnershipBlocked = true
		return round, err
	}
	snapshot := b.manager.collectTunSnapshot(ctx, netsnapshot.Options{Server: server})
	local := tunMandatoryEvidenceFromSnapshot(snapshot)
	round.Evidence.Mandatory.UplinkPath = local.UplinkPath
	round.Evidence.Mandatory.NetworkManager = local.NetworkManager
	round.Evidence.Mandatory.ResolvedDNS = local.ResolvedDNS
	plan.Snapshot = snapshot
	round.Plan = plan

	fingerprint, fingerprintErr := deriveTunUplinkFingerprint(snapshot, operatingSystemInterfaceIndex)
	if fingerprintErr != nil {
		round.Evidence.Mandatory.UplinkPath = tunLocalProofUnknown
		round.Cause = errors.Join(round.Cause, fmt.Errorf("observe current uplink fingerprint: %w", fingerprintErr))
	} else {
		round.Fingerprint = fingerprint
	}

	privacyPlan, err := privacyEnvelopePlanFromAuthority(source.Protection)
	if err != nil {
		round.OwnershipBlocked = true
		return round, fmt.Errorf("reconstruct Privacy Envelope authority: %w", err)
	}
	privacyExecutor := netexecutor.PrivacyEnvelopeExecutor{}
	privacyPresent, err := privacyExecutor.Exists(ctx, privacyPlan)
	if err != nil {
		round.Evidence.Mandatory.PrivacyEnvelope = tunLocalProofUnknown
		round.Cause = errors.Join(round.Cause, fmt.Errorf("observe Privacy Envelope: %w", err))
	} else if !privacyPresent {
		round.Evidence.Mandatory.PrivacyEnvelope = tunLocalProofViolated
		round.HardUnsafe = true
		round.Cause = errors.Join(round.Cause, errors.New("exact Privacy Envelope is absent"))
	} else if err := privacyExecutor.Verify(ctx, privacyPlan); err != nil {
		if ctx.Err() != nil {
			return round, ctx.Err()
		}
		// Command/inspection failure cannot prove a leak or grant cleanup
		// authority. A positively absent envelope above is the hard-unsafe case.
		round.Evidence.Mandatory.PrivacyEnvelope = tunLocalProofUnknown
		round.Cause = errors.Join(round.Cause, fmt.Errorf("verify exact Privacy Envelope: %w", err))
	} else {
		round.Evidence.Mandatory.PrivacyEnvelope = tunLocalProofProven
	}

	if err := b.manager.tunPlanExecutor().Verify(ctx, plan); err != nil {
		if ctx.Err() != nil {
			return round, ctx.Err()
		}
		round.Evidence.Mandatory.OwnedComposition = tunLocalProofViolated
		round.NeedsReconcile = true
		round.Cause = errors.Join(round.Cause, fmt.Errorf("verify exact Podlaz-owned TUN composition: %w", err))
	} else {
		round.Evidence.Mandatory.OwnedComposition = tunLocalProofProven
	}

	switch source.Kind {
	case protectedTunReplacementActive:
		round.Evidence.Mandatory.CoreTUN = tunLocalProofProven
	case protectedTunReplacementDegraded:
		round.Evidence.Mandatory.CoreTUN = tunLocalProofViolated
		round.NeedsReconcile = true
		round.Cause = errors.Join(round.Cause, errors.New("supervised TUN core exited"))
	default:
		round.OwnershipBlocked = true
		return round, fmt.Errorf("unsupported protected TUN source kind %q", source.Kind)
	}

	if err := maybeInjectE2ETunTerminalFailure(); err != nil {
		round.HardUnsafe = true
		round.Cause = errors.Join(round.Cause, err)
	}

	if source.Kind == protectedTunReplacementActive &&
		round.Evidence.Mandatory.OwnedComposition == tunLocalProofProven &&
		round.Evidence.Mandatory.PrivacyEnvelope == tunLocalProofProven {
		probes, err := collectTunRevalidationProbeEvidence(ctx, plan, b.networkClient)
		if err != nil {
			return round, err
		}
		round.Evidence.Probes = probes
		for _, probe := range probes {
			if !probe.Success && probe.Cause != nil {
				round.Cause = errors.Join(round.Cause, probe.Cause)
			}
		}
	}

	return round, nil
}

func reconciliationHealthRuntime(runtime *tunEvidenceRevalidationRuntime) *tunRevalidationRuntime {
	if runtime == nil {
		return nil
	}
	return runtime.tunRevalidationRuntime
}

func reconciliationTerminalFallbackCause(disposition tunAutomaticDisposition) error {
	if disposition.Cause != nil {
		return disposition.Cause
	}
	if disposition.Classification == api.TunHealthConnectivityFailed {
		return errors.New("bounded independent data-plane evidence remained unusable")
	}
	return errors.New("bounded TUN reconciliation reached a terminal safety boundary")
}

func newProductionTunAutomaticTerminalHandler(
	manager *XrayManager,
	store networkSessionStateStore,
	runtime *tunEvidenceRevalidationRuntime,
	refresh func(context.Context),
) tunAutomaticTerminalHandler {
	protection := privacyEnvelopeLifecycle{
		store:    store,
		executor: netexecutor.PrivacyEnvelopeExecutor{},
	}
	remainingNetwork := newPostPodlazNetworkVerifier()
	return tunAutomaticTerminalHandler{
		store: store,
		currentTransactionID: func() string {
			if manager == nil {
				return ""
			}
			state, _ := manager.activeTunRuntimeIdentity()
			return state.TransactionID
		},
		collect: func(ctx context.Context, plan interfacePlan, cause error) tunFailureDiagnosticSummary {
			return tunFailureDiagnosticSummary{}
		},
		teardown: func(ctx context.Context) error {
			if manager == nil {
				return errors.New("missing lifecycle manager for terminal teardown")
			}
			coordinator := networkSessionTeardownCoordinator{
				store:                  store,
				cleanupDataPlane:       manager.Disconnect,
				removeProtection:       protection.RemoveAfterDataPlaneCleanup,
				verifyRemainingNetwork: remainingNetwork.Verify,
			}
			_, err := coordinator.Teardown(ctx, networkSessionTeardownTerminal)
			if refresh != nil {
				refreshCtx, cancel := boundedStartupScanRefreshContext(ctx)
				refresh(refreshCtx)
				cancel()
			}
			return err
		},
		finalize: func(ctx context.Context, summary tunFailureDiagnosticSummary, status string) {
			if manager != nil {
				manager.finalizeTunFailureDiagnosticRollback(ctx, summary, status)
			}
		},
		markCleanupRequired: func(disposition tunAutomaticDisposition) {
			if runtime != nil {
				runtime.MarkAutomaticCleanupRequired(disposition)
			}
		},
		cleanupTimeout: tunRollbackCleanupTimeout,
	}
}
