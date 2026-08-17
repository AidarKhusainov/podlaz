package executor

import (
	"fmt"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

// VerifyNftablesTableOutput applies the same complete table-composition contract
// as NftablesExecutor.Verify to already-observed `nft -y list table` output.
// Callers remain responsible for executing the read-only observation command.
func VerifyNftablesTableOutput(plan planner.TunFirewallPlan, output string) error {
	if err := validateFirewallPlan(plan); err != nil {
		return err
	}
	family, table := firewallFamilyTable(plan)
	observed, err := parseOwnedNftTable(output, family, table)
	if err != nil {
		return fmt.Errorf("verify nftables table %s %s: %w", family, table, err)
	}
	if err := verifyExactNftChains(observed, plan); err != nil {
		return fmt.Errorf("verify nftables table %s %s: %w", family, table, err)
	}
	return nil
}
