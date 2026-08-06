package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestRenderTunPlanSummaryTreatsLocalNftablesInspectionAsDaemonRecheck(t *testing.T) {
	plan := renderTunPlanForTest()
	plan.Firewall.TableAction = planner.FirewallActionBlocked
	plan.Firewall.Reason = "nftables availability is unknown; firewall mutation is unsafe until nft can be inspected"

	var out bytes.Buffer
	renderTunPlanSummary(&out, plan, "profile-1", true)
	text := out.String()

	for _, want := range []string{
		"Status     Ready for daemon re-check",
		"Verify Xray TUN link",
		"podlaz0, MTU 1500, Xray-owned",
		"Local dry-run limitations",
		"nftables cannot be inspected as current user. The daemon will check this again before applying changes.",
		"Run: plz connect --mode tun profile-1",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Create TUN interface") {
		t.Fatalf("compact TUN plan must not promise daemon-created link ownership:\n%s", text)
	}
	if strings.Contains(text, "\nBlockers\n") {
		t.Fatalf("local dry-run nftables limitation must not be rendered as a hard blocker:\n%s", text)
	}
}

func TestRenderTunPlanSummaryKeepsMissingNftablesAsHardBlocker(t *testing.T) {
	plan := renderTunPlanForTest()
	plan.Firewall.TableAction = planner.FirewallActionBlocked
	plan.Firewall.Reason = "nftables availability is missing; firewall mutation is unsafe until nft can be inspected"

	var out bytes.Buffer
	renderTunPlanSummary(&out, plan, "profile-1", true)
	text := out.String()

	for _, want := range []string{
		"Status     Blocked",
		"Verify Xray TUN link",
		"Blockers",
		"Kill switch cannot be prepared yet",
		"Run: plz doctor",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Create TUN interface") {
		t.Fatalf("compact TUN plan must not promise daemon-created link ownership:\n%s", text)
	}
	if strings.Contains(text, "Run: plz connect --mode tun profile-1") {
		t.Fatalf("hard nftables blocker must not suggest connect as the next command:\n%s", text)
	}
}

func renderTunPlanForTest() planner.TunPlan {
	return planner.TunPlan{
		ProfileName: "Sweden",
		TunnelMode:  planner.TunTunnelMode,
		TunDevice: planner.TunDevicePlan{
			Name:   "podlaz0",
			MTU:    1500,
			Action: "verify",
			Reason: "Xray owns TUN link creation and lifetime; podlazd verifies the existing link before L3 mutations",
		},
		ServerBypass: planner.TunRoutePlan{
			Family:      "ipv4",
			Destination: "203.0.113.10/32",
			Table:       planner.MainRoutingTable,
			Interface:   "wlan0",
			Gateway:     "192.0.2.1",
			Action:      "add",
		},
		DNS: planner.TunDNSPlan{
			Backend:    planner.DNSBackendSystemdResolved,
			TargetLink: "podlaz0",
			Servers:    []string{"1.1.1.1"},
			Action:     planner.DNSActionConfigure,
		},
		Firewall: planner.TunFirewallPlan{
			Backend: planner.FirewallBackendNftables,
			Family:  "inet",
			Table:   "podlaz",
			KillSwitch: planner.TunKillSwitchPlan{
				Policy: planner.KillSwitchPolicySoft,
			},
		},
	}
}
