package recovery

import (
	"strconv"
	"strings"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

// recoveryRollbackMetadata preserves durable rollback ownership and adds only
// the narrow applying-state TUN-address syscall/persistence candidate. Desired
// routes, policy rules, DNS, nftables, and link intent never grant cleanup
// authority.
func recoveryRollbackMetadata(tx txstate.Transaction) txstate.RollbackMetadata {
	rollback := tx.Rollback
	desired := tx.DesiredPlan

	// Desired state validates the shape of a resource but never proves that the
	// daemon created it. The sole exception is the address syscall/persistence
	// crash window while applying: the exact bound link identity was persisted
	// before the mutable command and remains subject to fail-closed host checks.
	if len(rollback.TUNAddresses) == 0 && tx.State == txstate.TransactionApplying {
		address := desired.TUNAddress
		if address.InterfaceName == managedInterface &&
			address.LinkIndex > 0 &&
			address.LinkKind == "tun" &&
			address.AppearedAfterCore &&
			strings.TrimSpace(address.CIDR) == planner.DefaultTunIPv4CIDR &&
			ownedRollbackMetadata(address.Owner, netexecutor.OwnerTunAddress) {
			rollback.TUNAddresses = []txstate.TUNAddressRollback{{
				Family:            address.Family,
				InterfaceName:     address.InterfaceName,
				CIDR:              address.CIDR,
				Scope:             address.Scope,
				LinkIndex:         address.LinkIndex,
				LinkKind:          address.LinkKind,
				AppearedAfterCore: address.AppearedAfterCore,
				Owner:             address.Owner,
			}}
		}
	}
	return rollback
}

func desiredPolicyRuleRollback(step txstate.PlannedStep) (txstate.PolicyRuleRollback, bool) {
	if step.Kind != "policy-rule" || !ownedRollbackMetadata(step.Owner, netexecutor.OwnerPolicyRule) {
		return txstate.PolicyRuleRollback{}, false
	}
	fields := strings.Fields(step.Target)
	if len(fields) != 6 || fields[0] != "priority" || fields[4] != "lookup" {
		return txstate.PolicyRuleRollback{}, false
	}
	priority, err := strconv.Atoi(fields[1])
	if err != nil {
		return txstate.PolicyRuleRollback{}, false
	}
	rule := txstate.PolicyRuleRollback{Priority: priority, Table: fields[5], Owner: step.Owner}
	switch fields[2] {
	case "from":
		rule.From = fields[3]
	case "to":
		rule.To = fields[3]
	default:
		return txstate.PolicyRuleRollback{}, false
	}
	return rule, true
}

func reservedTunPolicyRule(rule txstate.PolicyRuleRollback) bool {
	defaultFrom := strings.TrimSpace(strings.TrimPrefix(planner.IPv4DefaultSelector, "from "))
	return rule.Priority == planner.TunRulePriority &&
		rule.Table == planner.TunRoutingTable &&
		strings.TrimSpace(rule.From) == defaultFrom &&
		strings.TrimSpace(rule.To) == "" &&
		strings.TrimSpace(rule.Mark) == ""
}

const normalizedSystemdResolvedBackend = "systemd-resolved"

func systemdResolvedBackend(backend string) bool {
	backend = strings.TrimSpace(backend)
	return backend == "" || backend == normalizedSystemdResolvedBackend || backend == planner.DNSBackendSystemdResolved
}
