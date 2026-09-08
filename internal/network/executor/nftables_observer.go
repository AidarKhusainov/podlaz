package executor

import (
	"context"
	"fmt"
)

// TableExists returns read-only structured presence evidence for the ordinary
// Podlaz-owned nftables table identity. Presence alone is never cleanup
// authority; destructive callers still need persisted authority plus exact
// semantic verification and a generation-guarded mutation.
func (e NftablesExecutor) TableExists(ctx context.Context, family, table string) (bool, error) {
	if err := validateOwnedFirewallTarget(family, table); err != nil {
		return false, err
	}
	present, err := observeNftTablePresence(ctx, e.Runner, family, table)
	if err != nil {
		return false, fmt.Errorf("observe nftables table %s %s presence: %w", family, table, err)
	}
	return present, nil
}
