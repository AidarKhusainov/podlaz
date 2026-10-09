package planner

import (
	"strconv"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/profile"
)

func TestImportedTypedXrayProtocolsPlanMarkedEgressWithoutEndpointBypass(t *testing.T) {
	for _, protocol := range []string{"vmess", "trojan", "shadowsocks"} {
		t.Run(protocol, func(t *testing.T) {
			p := profile.NewManual("synthetic", "example.com", 443, protocol)
			p.UserIdentity = "synthetic-password"
			s := snapshot.FakeResolvedDesktop()
			s.ServerRoute.Status = snapshot.StatusUnknown
			s.ServerRoute.Interface = ""
			s.ServerRoute.Gateway = ""

			plan, err := PlanTunForSession(p, s, TunOptions{})
			if err != nil {
				t.Fatalf("marked TUN plan: %v", err)
			}
			if plan.EgressMark == 0 || plan.ServerBypass.Destination != "" {
				t.Fatalf("typed protocol needs endpoint-independent mark and no bootstrap bypass")
			}
			want := "fwmark " + strconv.FormatUint(uint64(plan.EgressMark), 10)
			found := false
			for _, rule := range plan.PolicyRules {
				if rule.Selector == want && rule.Table == MainRoutingTable {
					found = true
				}
			}
			if !found {
				t.Fatal("marked main-table rule missing from protected typed TUN plan")
			}
		})
	}
}
