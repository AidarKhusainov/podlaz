package executor

import (
	"fmt"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

// VerifyNftablesTableOutput applies the same strict structured semantic
// composition contract as NftablesExecutor.Verify to already-observed `nft -j
// list table` output. Callers remain responsible for the read-only observation.
func VerifyNftablesTableOutput(plan planner.TunFirewallPlan, output string) error {
	if err := validateFirewallPlan(plan); err != nil {
		return err
	}
	family, table := firewallFamilyTable(plan)
	snapshot, err := parseNftTableJSON(output, family, table)
	if err != nil {
		return fmt.Errorf("verify nftables table %s %s: %w", family, table, err)
	}
	if err := verifyNftTableSnapshot(snapshot, plan); err != nil {
		return fmt.Errorf("verify nftables table %s %s: %w", family, table, err)
	}
	return nil
}
