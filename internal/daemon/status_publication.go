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

// statusForPublication returns a status snapshot with an exact active
// transaction id only when both lifecycle state and the published status stay
// on the same stable active TUN session across the read.
func (m *XrayManager) statusForPublication(ctx context.Context) api.StatusResponse {
	return m.statusForPublicationFrom(ctx, m.Status)
}

func (m *XrayManager) statusForPublicationFrom(ctx context.Context, statusFn func(context.Context) api.StatusResponse) api.StatusResponse {
	before := m.statusPublicationIdentity()
	status := statusFn(ctx)
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

func (m *XrayManager) activeTransactionID() string {
	identity := m.statusPublicationIdentity()
	if !identity.isActiveTun() {
		return ""
	}
	return identity.TransactionID
}
