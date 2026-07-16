package daemon

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

func TestProbeTunDiagnosticOwnershipRejectsMissingActiveTun(t *testing.T) {
	result := probeTunDiagnosticOwnership(context.Background(), tunDiagnosticInput{
		state: xrayState{
			Connection:    "active",
			Mode:          planner.ModeTun,
			TransactionID: "tx-test",
		},
		coreRunning: true,
		plan: planner.TunPlan{
			TunDevice: planner.TunDevicePlan{Name: netsnapshot.DefaultTunName},
		},
	})
	if result.Status != tundiag.ProbeFail || result.Classification != tundiag.ClassOwnershipMismatch {
		t.Fatalf("unexpected ownership result: %#v", result)
	}
}

func TestProbeTunDNSRoutingRejectsDuplicateResolvedLinkRecords(t *testing.T) {
	plan := planner.TunPlan{DNS: planner.TunDNSPlan{TargetLink: netsnapshot.DefaultTunName, Servers: []string{"1.1.1.1"}}}
	snapshot := netsnapshot.Snapshot{DNS: netsnapshot.DNS{ResolvedLinks: []netsnapshot.ResolvedLink{
		{Name: netsnapshot.DefaultTunName, DNSServers: []string{"1.1.1.1"}, DNSDomains: []string{"~."}, Protocols: []string{"+DefaultRoute"}},
		{Name: netsnapshot.DefaultTunName, DNSServers: []string{"1.1.1.1"}, DNSDomains: []string{"~."}, Protocols: []string{"+DefaultRoute"}},
	}}}
	result := probeTunDNSRouting(context.Background(), plan, snapshot)
	if result.Status != tundiag.ProbeFail || result.Classification != tundiag.ClassDNSApplyFailure || !strings.Contains(result.Error, "duplicate") {
		t.Fatalf("unexpected duplicate DNS result: %#v", result)
	}
}

func TestProbeTunDNSRoutingRejectsDNSServerOutsideTun(t *testing.T) {
	original := tunDiagnosticCommandRunner
	tunDiagnosticCommandRunner = func(context.Context, string, ...string) (tunDiagnosticCommandResult, error) {
		return tunDiagnosticCommandResult{
			command:  "ip -4 route get 1.1.1.1",
			stdout:   "1.1.1.1 via 192.0.2.1 dev eth0\n",
			exitCode: 0,
		}, nil
	}
	t.Cleanup(func() { tunDiagnosticCommandRunner = original })

	plan := planner.TunPlan{DNS: planner.TunDNSPlan{TargetLink: netsnapshot.DefaultTunName, Servers: []string{"1.1.1.1"}}}
	snapshot := netsnapshot.Snapshot{DNS: netsnapshot.DNS{ResolvedLinks: []netsnapshot.ResolvedLink{{
		Name: netsnapshot.DefaultTunName, DNSServers: []string{"1.1.1.1"}, DNSDomains: []string{"~."}, Protocols: []string{"+DefaultRoute"},
	}}}}
	result := probeTunDNSRouting(context.Background(), plan, snapshot)
	if result.Status != tundiag.ProbeFail || result.Classification != tundiag.ClassRouteFailure {
		t.Fatalf("unexpected DNS route result: %#v", result)
	}
	if !strings.Contains(result.Error, "expected podlaz0") {
		t.Fatalf("unexpected DNS route error: %q", result.Error)
	}
}

func TestPreferredAddressSelectsRequestedFamilyDeterministically(t *testing.T) {
	addresses := []string{"2001:db8::2", "192.0.2.20", "192.0.2.10", "2001:db8::1"}
	if got := preferredAddress(addresses, false); got != "192.0.2.10" {
		t.Fatalf("unexpected IPv4 address %q", got)
	}
	if got := preferredAddress(addresses, true); got != "2001:db8::1" {
		t.Fatalf("unexpected IPv6 address %q", got)
	}
}

func TestLookupTunRouteForAddressSelectsAddressFamily(t *testing.T) {
	original := tunDiagnosticCommandRunner
	var commands []string
	tunDiagnosticCommandRunner = func(_ context.Context, name string, args ...string) (tunDiagnosticCommandResult, error) {
		command := strings.TrimSpace(name + " " + strings.Join(args, " "))
		commands = append(commands, command)
		return tunDiagnosticCommandResult{command: command, stdout: fmt.Sprintf("%s dev podlaz0\n", args[len(args)-1])}, nil
	}
	t.Cleanup(func() { tunDiagnosticCommandRunner = original })

	if _, _, err := lookupTunRouteForAddress(context.Background(), "192.0.2.10"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := lookupTunRouteForAddress(context.Background(), "2001:db8::10"); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 2 || !strings.Contains(commands[0], "ip -4 route get") || !strings.Contains(commands[1], "ip -6 route get") {
		t.Fatalf("unexpected route commands: %v", commands)
	}
}
