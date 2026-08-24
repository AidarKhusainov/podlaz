package daemon

import (
	"context"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

type statusPublicationIdentity struct {
	Connection        string
	Mode              string
	ProfileID         string
	RuntimeConfigPath string
	TransactionID     string
}

func (i statusPublicationIdentity) isActiveTun() bool {
	return i.Connection == "active" &&
		i.Mode == planner.ModeTun &&
		i.ProfileID != "" &&
		i.RuntimeConfigPath != "" &&
		i.TransactionID != ""
}

func (m *XrayManager) statusForPublication(ctx context.Context) api.StatusResponse {
	return m.statusForPublicationFrom(ctx, m.Status)
}

func (m *XrayManager) statusForPublicationFrom(ctx context.Context, statusFn func(context.Context) api.StatusResponse) api.StatusResponse {
	before := m.statusPublicationIdentity()
	status := statusFn(ctx)
	status = guardNetworkSessionCleanupStatus(newNetworkSessionStateStore(m.runtimeDir(), nil), status)
	status = m.withProductTerminalReason(status)
	after := m.statusPublicationIdentity()
	status.ActiveTransactionID = ""
	if before != after || !after.isActiveTun() {
		return status
	}
	if status.Connection != "active" || status.Mode != planner.ModeTun {
		return status
	}
	if status.ProfileID != after.ProfileID || status.RuntimeConfigPath != after.RuntimeConfigPath {
		return status
	}
	status.ActiveTransactionID = after.TransactionID
	return status
}

func (m *XrayManager) withProductTerminalReason(status api.StatusResponse) api.StatusResponse {
	runtimeDir := m.runtimeDir()
	status.TerminalReason = resolveProductTerminalReason(
		status,
		newProductTerminalReasonStore(runtimeDir, nil),
		newBootAutostartAttemptStore(runtimeDir, nil),
	)
	return status
}

func resolveProductTerminalReason(
	status api.StatusResponse,
	reasonStore productTerminalReasonStore,
	attemptStore bootAutostartAttemptStore,
) api.TerminalReason {
	if status.Connection != "inactive" {
		return ""
	}

	resolution, exists, err := reasonStore.ResolveCurrent()
	if err != nil {
		// An unreadable current product outcome is ambiguous. Do not bypass it by
		// resurrecting older boot-attempt history as if the product outcome store
		// were absent.
		return ""
	}
	if exists {
		if resolution.Superseded {
			return ""
		}
		return resolution.Reason
	}

	// A terminal BootAutostartAttempt remains until reboot as one-attempt
	// authority. It is also a valid initial product reason only while no newer
	// explicit lifecycle has superseded that outcome.
	attempt, exists, err := attemptStore.LoadCurrent()
	if err != nil || !exists || attempt.State != bootAutostartAttemptTerminal {
		return ""
	}
	switch attempt.TerminalReason {
	case bootAutostartTerminalNetworkNotReady:
		return api.TerminalReasonBootNetworkNotReady
	case bootAutostartTerminalConnectFailed:
		return api.TerminalReasonVPNConnectFailed
	case bootAutostartTerminalSessionFailure:
		return api.TerminalReasonVPNRestoreFailed
	default:
		return ""
	}
}

func (m *XrayManager) statusPublicationIdentity() statusPublicationIdentity {
	m.mu.Lock()
	defer m.mu.Unlock()
	return statusPublicationIdentity{
		Connection:        m.state.Connection,
		Mode:              m.state.Mode,
		ProfileID:         m.state.ProfileID,
		RuntimeConfigPath: m.state.RuntimeConfigPath,
		TransactionID:     m.state.TransactionID,
	}
}
