package daemon

import (
	"fmt"
	"strings"
	"time"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func appliedStepsFromRollbackMetadataForTest(rollback txstate.RollbackMetadata, now time.Time) []txstate.AppliedStep {
	steps := make([]txstate.AppliedStep, 0,
		len(rollback.TUN)+len(rollback.TUNAddresses)+len(rollback.Routes)+len(rollback.PolicyRules)+len(rollback.DNS)+len(rollback.NFTables),
	)
	add := func(kind, target, owner string) {
		steps = append(steps, txstate.AppliedStep{
			Kind:      kind,
			Target:    strings.TrimSpace(target),
			Owner:     owner,
			AppliedAt: now.UTC(),
		})
	}
	for _, tun := range rollback.TUN {
		add("tun-device", tun.InterfaceName, netexecutor.OwnerTunDevice)
	}
	for _, address := range rollback.TUNAddresses {
		add("tun-address", fmt.Sprintf("%s@ifindex=%d:%s", strings.TrimSpace(address.InterfaceName), address.LinkIndex, strings.TrimSpace(address.CIDR)), netexecutor.OwnerTunAddress)
	}
	for _, route := range rollback.Routes {
		add("route", strings.TrimSpace(route.Table)+" "+strings.TrimSpace(route.CIDR), netexecutor.OwnerRoute)
	}
	for _, rule := range rollback.PolicyRules {
		selector := strings.TrimSpace(rule.From)
		if selector != "" {
			selector = "from " + selector
		} else if to := strings.TrimSpace(rule.To); to != "" {
			selector = "to " + to
		} else if mark := strings.TrimSpace(rule.Mark); mark != "" {
			selector = "fwmark " + mark
		}
		add("policy-rule", fmt.Sprintf("priority %d %s lookup %s", rule.Priority, selector, strings.TrimSpace(rule.Table)), netexecutor.OwnerPolicyRule)
	}
	for _, dns := range rollback.DNS {
		add("dns", dns.Link, netexecutor.OwnerDNS)
	}
	for _, nft := range rollback.NFTables {
		add("nftables", strings.TrimSpace(nft.Family)+" "+strings.TrimSpace(nft.Table), netexecutor.OwnerFirewall)
	}
	return steps
}
