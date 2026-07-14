package recovery

import (
	"strconv"
	"strings"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

// recoveryRollbackMetadata fills only rollback categories in podlaz-reserved
// namespaces that the daemon may not have persisted before an abrupt stop.
// Main-table server bypass state is intentionally excluded because desired state
// alone cannot prove that podlaz, rather than another process, created it.
func recoveryRollbackMetadata(tx txstate.Transaction) txstate.RollbackMetadata {
	rollback := tx.Rollback
	desired := tx.DesiredPlan

	if len(rollback.TUN) == 0 && desired.TUN.InterfaceName == managedInterface && ownedRollbackMetadata(desired.TUN.Owner, netexecutor.OwnerTunDevice) {
		rollback.TUN = []txstate.TUNRollback{{InterfaceName: desired.TUN.InterfaceName, Owner: desired.TUN.Owner}}
	}
	if len(rollback.Routes) == 0 {
		for _, route := range desired.Routes {
			if route.Operation != "add" || route.Table != planner.TunRoutingTable || !ownedRollbackMetadata(route.Owner, netexecutor.OwnerRoute) {
				continue
			}
			rollback.Routes = append(rollback.Routes, txstate.RouteRollback{
				Table: route.Table,
				CIDR:  route.CIDR,
				Via:   route.Via,
				Dev:   route.Dev,
				Owner: route.Owner,
			})
		}
	}
	if len(rollback.PolicyRules) == 0 {
		for _, step := range desired.Steps {
			rule, ok := desiredPolicyRuleRollback(step)
			if !ok || !reservedTunPolicyRule(rule) {
				continue
			}
			rollback.PolicyRules = append(rollback.PolicyRules, rule)
		}
	}
	if len(rollback.DNS) == 0 && desired.DNS.Link == managedInterface && systemdResolvedBackend(desired.DNS.Backend) && ownedRollbackMetadata(desired.DNS.Owner, netexecutor.OwnerDNS) {
		rollback.DNS = []txstate.DNSRollback{{
			Backend:       normalizedSystemdResolvedBackend,
			Link:          desired.DNS.Link,
			SearchDomains: append([]string{}, desired.DNS.SearchDomains...),
			Owner:         desired.DNS.Owner,
		}}
	}
	if len(rollback.NFTables) == 0 && isManagedNFTTarget(desired.NFT.Family, desired.NFT.Table) && ownedRollbackMetadata(desired.NFT.Owner, netexecutor.OwnerFirewall) {
		rollback.NFTables = []txstate.NFTablesRollback{{Family: desired.NFT.Family, Table: desired.NFT.Table, Owner: desired.NFT.Owner}}
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
	return rule.Priority == planner.TunRulePriority &&
		rule.Table == planner.TunRoutingTable &&
		strings.TrimSpace(rule.From) == planner.IPv4DefaultSelector &&
		strings.TrimSpace(rule.To) == "" &&
		strings.TrimSpace(rule.Mark) == ""
}

const normalizedSystemdResolvedBackend = "systemd-resolved"

func systemdResolvedBackend(backend string) bool {
	backend = strings.TrimSpace(backend)
	return backend == "" || backend == normalizedSystemdResolvedBackend || backend == planner.DNSBackendSystemdResolved
}
