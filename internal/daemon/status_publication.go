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
	if status.Connection != "inactive" {
		return status
	}
	runtimeDir := m.runtimeDir()
	reasonStore := newProductTerminalReasonStore(runtimeDir, nil)
	if reason, exists, err := reasonStore.LoadCurrent(); err == nil && exists {
		status.TerminalReason = reason
		return status
	}
	attemptStore := newBootAutostartAttemptStore(runtimeDir, nil)
	attempt, exists, err := attemptStore.LoadCurrent()
	if err != nil || !exists || attempt.State != bootAutostartAttemptTerminal {
		return status
	}
	switch attempt.TerminalReason {
	case bootAutostartTerminalNetworkNotReady:
		status.TerminalReason = api.TerminalReasonBootNetworkNotReady
	case bootAutostartTerminalConnectFailed:
		status.TerminalReason = api.TerminalReasonVPNConnectFailed
	case bootAutostartTerminalSessionFailure:
		status.TerminalReason = api.TerminalReasonVPNRestoreFailed
	}
	return status
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
