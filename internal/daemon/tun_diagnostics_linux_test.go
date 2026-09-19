package daemon

import (
	"context"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

func TestParseTunDiagnosticRouteAndPolicyRule(t *testing.T) {
	route := parseTunDiagnosticRoute("1.1.1.1 dev podlaz0 table 51820 src 10.0.0.2\n")
	if route.Interface != "podlaz0" || route.Table != "51820" {
		t.Fatalf("unexpected route evidence: %#v", route)
	}
	if !tunDiagnosticHasPolicyRule("10000: from all lookup 51820\n", planner.TunPolicyRulePlan{
		Priority: planner.TunRulePriority,
		Selector: planner.IPv4DefaultSelector,
		Table:    "51820",
	}) {
		t.Fatal("expected podlaz policy rule to be detected")
	}
}

func TestProbeTunIPv4RouteUsesAllocatedFullTunnelRule(t *testing.T) {
	original := tunDiagnosticCommandRunner
	t.Cleanup(func() { tunDiagnosticCommandRunner = original })

	plan := planner.TunPlan{
		TunDevice: planner.TunDevicePlan{Name: netsnapshot.DefaultTunName},
		PolicyRules: []planner.TunPolicyRulePlan{
			{Priority: 9997, Selector: "to 203.0.113.10/32", Table: planner.MainRoutingTable},
			{Priority: 9998, Selector: planner.IPv4DefaultSelector, Table: "51821"},
		},
	}

	ruleOutput := "9997: from all to 203.0.113.10 lookup main\n9998: from all lookup 51821\n"
	tunDiagnosticCommandRunner = func(_ context.Context, name string, args ...string) (tunDiagnosticCommandResult, error) {
		command := strings.TrimSpace(name + " " + strings.Join(args, " "))
		switch command {
		case "ip -4 route get 1.1.1.1":
			return tunDiagnosticCommandResult{command: command, stdout: "1.1.1.1 dev podlaz0 table 51821\n", exitCode: 0}, nil
		case "ip -4 rule show":
			return tunDiagnosticCommandResult{command: command, stdout: ruleOutput, exitCode: 0}, nil
		default:
			t.Fatalf("unexpected diagnostic command: %s", command)
			return tunDiagnosticCommandResult{}, nil
		}
	}

	result := probeTunIPv4Route(context.Background(), plan)
	if result.Status != tundiag.ProbePass {
		t.Fatalf("reallocated full-tunnel rule must pass diagnostics: %#v", result)
	}

	ruleOutput = "9998: from all lookup 51822\n"
	result = probeTunIPv4Route(context.Background(), plan)
	if result.Status != tundiag.ProbeFail || result.Classification != tundiag.ClassPolicyRuleFailure {
		t.Fatalf("wrong table at allocated priority must fail exact diagnostics: %#v", result)
	}
}

func TestProbeTunServerBypassUsesAllocatedPolicyRule(t *testing.T) {
	original := tunDiagnosticCommandRunner
	t.Cleanup(func() { tunDiagnosticCommandRunner = original })

	plan := planner.TunPlan{
		ServerBypass: planner.TunRoutePlan{
			Destination: "203.0.113.10/32",
			Table:       planner.MainRoutingTable,
			Interface:   "host0",
			Gateway:     "192.0.2.1",
		},
		PolicyRules: []planner.TunPolicyRulePlan{
			{Priority: 9997, Selector: "to 203.0.113.10/32", Table: planner.MainRoutingTable},
			{Priority: 9998, Selector: planner.IPv4DefaultSelector, Table: "51821"},
		},
	}
	ruleOutput := "9997: from all to 203.0.113.10 lookup main\n9998: from all lookup 51821\n"
	tunDiagnosticCommandRunner = func(_ context.Context, name string, args ...string) (tunDiagnosticCommandResult, error) {
		command := strings.TrimSpace(name + " " + strings.Join(args, " "))
		switch command {
		case "ip -4 route get 203.0.113.10":
			return tunDiagnosticCommandResult{command: command, stdout: "203.0.113.10 via 192.0.2.1 dev host0 table main\n", exitCode: 0}, nil
		case "ip -4 rule show":
			return tunDiagnosticCommandResult{command: command, stdout: ruleOutput, exitCode: 0}, nil
		default:
			t.Fatalf("unexpected diagnostic command: %s", command)
			return tunDiagnosticCommandResult{}, nil
		}
	}

	result := probeTunServerBypassPath(context.Background(), plan)
	if result.Status != tundiag.ProbePass {
		t.Fatalf("reallocated server bypass rule must pass diagnostics: %#v", result)
	}

	ruleOutput = "9999: from all to 203.0.113.10 lookup main\n"
	result = probeTunServerBypassPath(context.Background(), plan)
	if result.Status != tundiag.ProbeFail || result.Classification != tundiag.ClassPolicyRuleFailure {
		t.Fatalf("historical priority must not replace allocated bypass rule: %#v", result)
	}
}

func TestProbeTunDNSStateRejectsForeignRouteOnlyOwner(t *testing.T) {
	result := probeTunDNSState(planner.TunPlan{DNS: planner.TunDNSPlan{TargetLink: "podlaz0", Servers: []string{"1.1.1.1"}}}, netsnapshot.Snapshot{
		DNS: netsnapshot.DNS{ResolvedLinks: []netsnapshot.ResolvedLink{
			{Name: "podlaz0", DNSServers: []string{"1.1.1.1"}, DNSDomains: []string{"~."}, Protocols: []string{"+DefaultRoute"}},
			{Name: "wg0", DNSDomains: []string{"~."}},
		}},
	})
	if result.Status != tundiag.ProbeFail || result.Classification != tundiag.ClassForeignDNSConflict {
		t.Fatalf("unexpected DNS state result: %#v", result)
	}
}

func TestTunDiagnosticCappedBufferDoesNotGrowPastLimit(t *testing.T) {
	buffer := newTunDiagnosticCappedBuffer(8)
	input := strings.Repeat("x", 64)
	count, err := buffer.Write([]byte(input))
	if err != nil || count != len(input) {
		t.Fatalf("unexpected write result count=%d err=%v", count, err)
	}
	if len(buffer.String()) != 8 {
		t.Fatalf("expected 8 stored bytes, got %d", len(buffer.String()))
	}
}
