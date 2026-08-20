package daemon

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

type protectedTunReplacementSourceKind string

const (
	protectedTunReplacementActive   protectedTunReplacementSourceKind = "active"
	protectedTunReplacementDegraded protectedTunReplacementSourceKind = "degraded"
)

type protectedTunReplacementSource struct {
	Kind        protectedTunReplacementSourceKind
	SessionID   string
	Request     api.ConnectRequest
	Protection  networkSessionProtection
	Transaction txstate.Transaction
}

// loadProtectedTunReplacementForRequest recognizes a durable protected
// replacement transition and proves the generation being replaced. It is
// read-only: no networking or lifecycle state is mutated here.
func (m *XrayManager) loadProtectedTunReplacementForRequest(
	store networkSessionStateStore,
	target api.ConnectRequest,
) (protectedTunReplacementSource, bool, error) {
	if m == nil || api.NormalizeHandoffPolicy(target.Handoff) != api.HandoffReplacePodlaz {
		return protectedTunReplacementSource{}, false, nil
	}
	state, exists, err := store.Load()
	if err != nil {
		return protectedTunReplacementSource{}, false, fmt.Errorf("load protected replacement Network Session: %w", err)
	}
	if !exists || state.Replacement == nil || state.Protection == nil {
		return protectedTunReplacementSource{}, false, nil
	}
	if state.Intent != networkSessionIntentResume {
		return protectedTunReplacementSource{}, false, fmt.Errorf("protected replacement cancelled by Network Session intent %q", state.Intent)
	}
	if !reflect.DeepEqual(state.Request, target) {
		return protectedTunReplacementSource{}, false, errors.New("protected replacement target no longer matches durable Network Session request")
	}
	managerState, process := m.activeTunRuntimeIdentity()
	source, err := loadProtectedTunReplacementSource(m.runtimeDir(), managerState, process, state)
	if err != nil {
		return protectedTunReplacementSource{}, false, err
	}
	return source, true, nil
}

// prepareProtectedTunReplacement widens the exact session-wide Privacy Envelope
// before the source generation can be cleaned up. The source proof is rechecked
// against durable session/transaction authority immediately before the nftables
// mutation, preventing stale automatic repair authority from widening a newer
// session's barrier.
func prepareProtectedTunReplacement(
	ctx context.Context,
	store networkSessionStateStore,
	source protectedTunReplacementSource,
	targetPlan planner.TunPlan,
	executor privacyEnvelopeLifecycleExecutor,
) (*privacyEnvelopeLifecycle, error) {
	if executor == nil {
		return nil, errors.New("protected replacement requires a Privacy Envelope executor")
	}
	state, exists, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("reload Network Session before protected replacement: %w", err)
	}
	if !exists || state.SessionID != source.SessionID || state.Intent != networkSessionIntentResume {
		return nil, errors.New("protected replacement source was superseded before Privacy Envelope preparation")
	}
	if state.Replacement == nil || state.Protection == nil || state.Replacement.PreviousProtection == nil {
		return nil, errors.New("protected replacement lost durable previous-generation authority")
	}
	if !reflect.DeepEqual(state.Replacement.PreviousRequest, source.Request) ||
		!samePrivacyEnvelopeIdentity(*state.Replacement.PreviousProtection, source.Protection) ||
		!samePrivacyEnvelopeIdentity(*state.Protection, source.Protection) {
		return nil, errors.New("protected replacement source identity changed before Privacy Envelope preparation")
	}

	tx, _, err := (txstate.TransactionStore{RuntimeDir: store.runtimeDir}).Load(source.Transaction.ID)
	if err != nil {
		return nil, fmt.Errorf("reload protected replacement source transaction: %w", err)
	}
	if tx.Owner != txstate.TransactionOwner || tx.ID != source.Transaction.ID || tx.ProfileID != source.Transaction.ProfileID || !tx.RequiresRecovery() {
		return nil, errors.New("protected replacement source transaction authority changed before Privacy Envelope preparation")
	}

	lifecycle := &privacyEnvelopeLifecycle{store: store, executor: executor}
	if err := lifecycle.PrepareReplacement(ctx, targetPlan); err != nil {
		return nil, err
	}
	return lifecycle, nil
}

func productionProtectedTunReplacementLifecycle(
	ctx context.Context,
	store networkSessionStateStore,
	source protectedTunReplacementSource,
	targetPlan planner.TunPlan,
) (*privacyEnvelopeLifecycle, error) {
	return prepareProtectedTunReplacement(ctx, store, source, targetPlan, netexecutor.PrivacyEnvelopeExecutor{})
}

// loadProtectedTunReplacementSource proves that a rebuild belongs to the exact
// durable protected Network Session. A degraded source is deliberately distinct
// from an active source: it has no supervised process to preserve, but it still
// has exact persisted session, Privacy Envelope, and transaction authority for
// the obsolete data-plane generation.
func loadProtectedTunReplacementSource(
	runtimeDir string,
	managerState xrayState,
	process *tunRuntimeProcessIdentity,
	sessionState networkSessionState,
) (protectedTunReplacementSource, error) {
	if err := validateNetworkSessionState(sessionState); err != nil {
		return protectedTunReplacementSource{}, fmt.Errorf("validate protected Network Session source: %w", err)
	}
	if sessionState.Intent != networkSessionIntentResume {
		return protectedTunReplacementSource{}, fmt.Errorf("protected replacement source requires resume intent, found %q", sessionState.Intent)
	}
	if sessionState.Request.Mode != planner.ModeTun {
		return protectedTunReplacementSource{}, errors.New("protected replacement source is not a TUN Network Session")
	}
	if sessionState.Protection == nil {
		return protectedTunReplacementSource{}, errors.New("protected replacement source has no Privacy Envelope authority")
	}
	if err := validateNetworkSessionProtection(*sessionState.Protection); err != nil {
		return protectedTunReplacementSource{}, fmt.Errorf("validate protected replacement Privacy Envelope: %w", err)
	}

	sourceRequest := sessionState.Request
	sourceProtection := sessionState.Protection
	if sessionState.Replacement != nil {
		sourceRequest = sessionState.Replacement.PreviousRequest
		sourceProtection = sessionState.Replacement.PreviousProtection
		if sourceProtection == nil {
			return protectedTunReplacementSource{}, errors.New("protected replacement transition has no previous Privacy Envelope authority")
		}
		if !samePrivacyEnvelopeIdentity(*sessionState.Protection, *sourceProtection) {
			return protectedTunReplacementSource{}, errors.New("protected replacement source Privacy Envelope identity changed")
		}
	}
	if sourceRequest.Mode != planner.ModeTun {
		return protectedTunReplacementSource{}, errors.New("protected replacement source request is not TUN mode")
	}
	if err := validateNetworkSessionProtection(*sourceProtection); err != nil {
		return protectedTunReplacementSource{}, fmt.Errorf("validate protected replacement source Privacy Envelope: %w", err)
	}
	if sourceProtection.State != networkSessionProtectionArmed {
		return protectedTunReplacementSource{}, fmt.Errorf("protected replacement source Privacy Envelope state is %q, want %q", sourceProtection.State, networkSessionProtectionArmed)
	}

	transactionID := strings.TrimSpace(managerState.TransactionID)
	if managerState.Mode != planner.ModeTun || transactionID == "" {
		return protectedTunReplacementSource{}, errors.New("protected replacement source has no exact TUN transaction identity")
	}
	profileID := strings.TrimSpace(sourceRequest.Profile.ID)
	if profileID == "" || strings.TrimSpace(managerState.ProfileID) != profileID {
		return protectedTunReplacementSource{}, errors.New("protected replacement manager profile does not match source Network Session request")
	}

	tx, _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Load(transactionID)
	if err != nil {
		return protectedTunReplacementSource{}, fmt.Errorf("load protected replacement transaction %s: %w", transactionID, err)
	}
	if tx.Owner != txstate.TransactionOwner || tx.ID != transactionID || tx.Mode != planner.ModeTun || tx.ProfileID != profileID {
		return protectedTunReplacementSource{}, errors.New("protected replacement transaction identity does not match source Network Session")
	}
	if !tx.RequiresRecovery() {
		return protectedTunReplacementSource{}, fmt.Errorf("protected replacement transaction state %q has no recovery authority", tx.State)
	}
	if tx.DesiredPlan.Core.Owner != txstate.TransactionOwner ||
		tx.DesiredPlan.Core.ProcessLabel != "xray" ||
		strings.TrimSpace(tx.DesiredPlan.Core.RuntimeConfigPath) == "" ||
		filepath.Clean(tx.DesiredPlan.Core.RuntimeConfigPath) != filepath.Clean(managerState.RuntimeConfigPath) {
		return protectedTunReplacementSource{}, errors.New("protected replacement transaction core identity does not match manager state")
	}
	if tx.DesiredPlan.TUN.Owner != xrayTunInboundOwner ||
		strings.TrimSpace(tx.DesiredPlan.TUN.InterfaceName) == "" ||
		tx.DesiredPlan.TUN.InterfaceName != sourceProtection.TunInterface {
		return protectedTunReplacementSource{}, errors.New("protected replacement TUN identity does not match source Privacy Envelope")
	}

	kind, err := classifyProtectedTunReplacementSource(managerState, process, tx)
	if err != nil {
		return protectedTunReplacementSource{}, err
	}
	return protectedTunReplacementSource{
		Kind:        kind,
		SessionID:   sessionState.SessionID,
		Request:     sourceRequest,
		Protection:  cloneNetworkSessionProtection(*sourceProtection),
		Transaction: tx,
	}, nil
}

func classifyProtectedTunReplacementSource(
	managerState xrayState,
	process *tunRuntimeProcessIdentity,
	tx txstate.Transaction,
) (protectedTunReplacementSourceKind, error) {
	switch managerState.Connection {
	case "active":
		if tx.State != txstate.TransactionCommitted {
			return "", fmt.Errorf("active protected replacement transaction state is %q, want committed", tx.State)
		}
		if err := verifyCommittedTunRuntimeIdentity(managerState, process, tx); err != nil {
			return "", fmt.Errorf("prove active protected replacement source: %w", err)
		}
		return protectedTunReplacementActive, nil
	case "error (core exited)":
		if process != nil {
			return "", errors.New("degraded protected replacement source unexpectedly has a live supervised process identity")
		}
		if !tx.RequiresRecovery() {
			return "", fmt.Errorf("degraded protected replacement transaction state %q is not recoverable", tx.State)
		}
		return protectedTunReplacementDegraded, nil
	default:
		return "", fmt.Errorf("unsupported protected replacement manager connection state %q", managerState.Connection)
	}
}
