package daemon

import (
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestTunRevalidationPlanRejectsAmbiguousMultiChainFirewallMetadata(t *testing.T) {
	tx := issue245DesiredRevalidationTransaction()
	tx.DesiredPlan.NFT.Chains = append(tx.DesiredPlan.NFT.Chains, txstate.NFTChainPlan{
		Name:     "forward",
		Type:     "filter",
		Hook:     "forward",
		Priority: 0,
		Policy:   "accept",
		Owner:    netexecutor.OwnerFirewall,
		Rules:    []string{"oifname podlaz0 accept owner podlaz:firewall:tun-egress"},
	})

	if _, err := tunRevalidationPlanFromTransaction(tx); err == nil {
		t.Fatal("expected ambiguous multi-chain firewall metadata to fail closed")
	}
}
