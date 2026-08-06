package daemon

import (
	"fmt"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

// requireTunPlanMutationFreePreflight rejects plan-level blockers before any
// destructive handoff can stop an active session, stop a known VPN, create a
// transaction, or start Xray. Ownership validation actions are allowed here;
// true blocked/unknown planner states are not.
func requireTunPlanMutationFreePreflight(plan planner.TunPlan) error {
	if plan.DNS.Action == planner.DNSActionBlocked {
		return fmt.Errorf("TUN DNS preflight blocked before handoff: %s", strings.TrimSpace(plan.DNS.Reason))
	}
	if plan.Firewall.TableAction == planner.FirewallActionBlocked {
		return fmt.Errorf("TUN firewall preflight blocked before handoff: %s", strings.TrimSpace(plan.Firewall.Reason))
	}
	if plan.Firewall.TableAction == planner.FirewallActionValidate {
		// validate-or-replace is an ownership-validation state, not an early
		// blocker. The later ownership preflight decides whether active-owned
		// nftables can be excluded or unrelated stale state must block.
		return nil
	}
	return nil
}
