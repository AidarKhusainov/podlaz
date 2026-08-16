package doctor

import (
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

// LifecycleState describes the daemon-authoritative connection lifecycle used
// to interpret managed-looking resources. The zero value is intentionally
// unknown so local fallback diagnostics remain fail-closed.
type LifecycleState uint8

const (
	LifecycleUnknown LifecycleState = iota
	LifecycleInactive
	LifecycleActiveProxy
	LifecycleActiveTUN
)

// ManagedResourceOwnership describes what daemon-authoritative persisted state
// proves about a managed-looking resource before the current host observation.
// ExpectedOwned means the transaction contains exact expected identity or
// composition; it is not current ownership proof. The resource-specific doctor
// inspector must verify the live object before reporting it healthy.
type ManagedResourceOwnership uint8

const (
	ManagedResourceUnknown ManagedResourceOwnership = iota
	ManagedResourceExpectedAbsent
	ManagedResourceExpectedOwned
	ManagedResourceUnproven
)

// LifecycleDiagnosticContext is typed daemon authority supplied to otherwise
// generic read-only doctor checks. It contains no mutation capability.
type LifecycleDiagnosticContext struct {
	State                      LifecycleState
	TransactionID              string
	TransactionState           txstate.TransactionState
	TransactionRequiresCleanup bool

	Interface          ManagedResourceOwnership
	InterfaceLinkIndex int
	InterfaceLinkKind  string
	NFTTable           ManagedResourceOwnership
	NFTPlan            *planner.TunFirewallPlan
}
