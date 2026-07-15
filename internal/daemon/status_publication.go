package daemon

import (
	"context"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

// statusForPublication returns a status snapshot with an exact active
// transaction id only when the lifecycle state stayed stable across the read.
// A concurrent transition clears the id so recovery filtering fails closed.
func (m *XrayManager) statusForPublication(ctx context.Context) api.StatusResponse {
	before := m.activeTransactionID()
	status := m.Status(ctx)
	after := m.activeTransactionID()
	if before == after {
		status.ActiveTransactionID = after
	}
	return status
}

func (m *XrayManager) activeTransactionID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state.TransactionID
}
