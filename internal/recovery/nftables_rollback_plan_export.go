package recovery

import (
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

// ExactNftablesRollbackPlan reconstructs exact nftables composition only when
// durable desired state is backed by the matching transaction rollback tuple.
func ExactNftablesRollbackPlan(tx txstate.Transaction, entry txstate.NFTablesRollback) (planner.TunFirewallPlan, error) {
	return exactNftablesRollbackPlan(tx, entry)
}
