package executor

import (
	"context"
	"errors"
	"fmt"
)

// PrivacyEnvelopeTableExists is read-only allocation evidence. A generated
// table name can be reported as occupied, but this method deliberately returns
// no cleanup authority and never mutates the object it finds.
func (e PrivacyEnvelopeExecutor) PrivacyEnvelopeTableExists(ctx context.Context, family, table string) (bool, error) {
	if family != ownedNFTFamily {
		return false, fmt.Errorf("privacy envelope candidate has unsupported nftables family %q", family)
	}
	if !privacyEnvelopeTableNamePattern.MatchString(table) {
		return false, errors.New("privacy envelope candidate has invalid table identity")
	}
	present, err := observeNftTablePresence(ctx, e.Runner, family, table)
	if err != nil {
		return false, fmt.Errorf("observe privacy envelope candidate %s %s: %w", family, table, err)
	}
	return present, nil
}
