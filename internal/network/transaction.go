package network

import "time"

// TransactionState describes the lifecycle state of a privileged networking mutation.
type TransactionState string

const (
	TransactionPlanned    TransactionState = "planned"
	TransactionApplied    TransactionState = "applied"
	TransactionCommitted  TransactionState = "committed"
	TransactionRolledBack TransactionState = "rolled_back"
)

// Snapshot is a point-in-time view of host networking state.
//
// Future implementations should include routes, rules, DNS state, nftables
// tables, managed interfaces, and daemon-owned process state.
type Snapshot struct {
	CreatedAt time.Time
	Notes     []string
}

// Plan is a dry-run representation of changes podlaz intends to apply.
type Plan struct {
	InterfaceName string
	Routes        []string
	Rules         []string
	DNS           []string
	Firewall      []string
}

// Transaction couples the pre-change snapshot with the intended plan.
type Transaction struct {
	ID        string
	State     TransactionState
	Snapshot  Snapshot
	Plan      Plan
	CreatedAt time.Time
}

// NewTransaction creates a planned transaction without mutating host state.
func NewTransaction(id string, snapshot Snapshot, plan Plan) Transaction {
	return Transaction{
		ID:        id,
		State:     TransactionPlanned,
		Snapshot:  snapshot,
		Plan:      plan,
		CreatedAt: time.Now().UTC(),
	}
}
