package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func (l *networkSessionLifecycle) publishTerminalConnectReason(
	ctx context.Context,
	request api.ConnectRequest,
	connectErr error,
	previousExists bool,
) error {
	if !terminalConnectReasonEligible(ctx, request, connectErr, previousExists) {
		return nil
	}
	store := l.productTerminalReasonStore()
	if err := store.Set(api.TerminalReasonVPNConnectFailed); err != nil {
		return fmt.Errorf("persist product terminal reason after failed connect: %w", err)
	}
	return nil
}

func terminalConnectReasonEligible(
	ctx context.Context,
	request api.ConnectRequest,
	connectErr error,
	previousExists bool,
) bool {
	if connectErr == nil || previousExists {
		return false
	}
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	if errors.Is(connectErr, errConnectionAlreadyActive) || errors.Is(connectErr, errFullTunnelConnectionBecameActive) {
		return false
	}

	switch strings.TrimSpace(request.Mode) {
	case planner.ModeProxyOnly:
		// Proxy-only does not mutate host routing/DNS/firewall state. With no
		// previous Network Session and no active-conflict/cancellation signal, a
		// returned Connect error is a clean terminal outcome for this admitted
		// product lifecycle epoch.
		return true
	case planner.ModeTun:
		var phased tunFailurePhaseError
		if !errors.As(connectErr, &phased) {
			return false
		}
		switch strings.TrimSpace(phased.rollbackStatus) {
		case "not-started", "completed":
			return true
		default:
			// failed/unknown rollback is not conclusively Disconnected and must
			// not be promoted into a stable terminal product reason.
			return false
		}
	default:
		return false
	}
}
