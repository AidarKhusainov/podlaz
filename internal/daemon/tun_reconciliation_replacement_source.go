package daemon

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/api"
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
	if sessionState.Protection.State != networkSessionProtectionArmed {
		return protectedTunReplacementSource{}, fmt.Errorf("protected replacement source Privacy Envelope state is %q, want %q", sessionState.Protection.State, networkSessionProtectionArmed)
	}

	transactionID := strings.TrimSpace(managerState.TransactionID)
	if managerState.Mode != planner.ModeTun || transactionID == "" {
		return protectedTunReplacementSource{}, errors.New("protected replacement source has no exact TUN transaction identity")
	}
	profileID := strings.TrimSpace(sessionState.Request.Profile.ID)
	if profileID == "" || strings.TrimSpace(managerState.ProfileID) != profileID {
		return protectedTunReplacementSource{}, errors.New("protected replacement manager profile does not match Network Session request")
	}

	tx, _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Load(transactionID)
	if err != nil {
		return protectedTunReplacementSource{}, fmt.Errorf("load protected replacement transaction %s: %w", transactionID, err)
	}
	if tx.Owner != txstate.TransactionOwner || tx.ID != transactionID || tx.Mode != planner.ModeTun || tx.ProfileID != profileID {
		return protectedTunReplacementSource{}, errors.New("protected replacement transaction identity does not match Network Session")
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
		tx.DesiredPlan.TUN.InterfaceName != sessionState.Protection.TunInterface {
		return protectedTunReplacementSource{}, errors.New("protected replacement TUN identity does not match Privacy Envelope")
	}

	kind, err := classifyProtectedTunReplacementSource(managerState, process, tx)
	if err != nil {
		return protectedTunReplacementSource{}, err
	}
	return protectedTunReplacementSource{
		Kind:        kind,
		SessionID:   sessionState.SessionID,
		Request:     sessionState.Request,
		Protection:  cloneNetworkSessionProtection(*sessionState.Protection),
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
