package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

const productionTunOnelineLinkForTest = `7: podlaz0: <POINTOPOINT,NOARP,UP,LOWER_UP> mtu 1500 qdisc fq_codel state UNKNOWN mode DEFAULT group default qlen 500 link/none tun type tun pi off vnet_hdr on persist off`

func TestTunHandoffPreflightIgnoresCurrentScopesWithoutConfigurationEvidence(t *testing.T) {
	for _, currentScopes := range [][]string{{"none"}, {"DNS"}} {
		snapshot := netsnapshot.Snapshot{DNS: netsnapshot.DNS{ResolvedLinks: []netsnapshot.ResolvedLink{{
			Name:          "wg-example",
			CurrentScopes: currentScopes,
			DNSDomains:    []string{defaultDNSRouteDomain},
		}}}}

		if err := preflightTunOwnership(snapshot, api.HandoffBlock); err != nil {
			t.Fatalf("Current Scopes %v must not change handoff verdict without stable DNS configuration: %v", currentScopes, err)
		}
	}
}

func TestTunHandoffPreflightUsesStableDNSConfigurationRegardlessOfCurrentScopes(t *testing.T) {
	for _, currentScopes := range [][]string{{"none"}, {"DNS"}} {
		snapshot := netsnapshot.Snapshot{DNS: netsnapshot.DNS{ResolvedLinks: []netsnapshot.ResolvedLink{{
			Name:          "wg-example",
			CurrentScopes: currentScopes,
			Protocols:     []string{"+DefaultRoute"},
			DNSDomains:    []string{defaultDNSRouteDomain},
		}}}}

		if err := preflightTunOwnership(snapshot, api.HandoffBlock); !isTunHandoffBlocker(err) {
			t.Fatalf("stable route-only DNS configuration must block handoff for Current Scopes %v: %v", currentScopes, err)
		}
	}
}

func TestProductionTunTransactionPersistsAndRollsBackBasePartialOwnership(t *testing.T) {
	t.Setenv(e2eTunHookGateEnv, "")
	t.Setenv(e2eTunHookPhaseEnv, "")

	tests := []struct {
		name            string
		plan            planner.TunPlan
		failCommand     string
		wantKind        string
		wantOwner       string
		rollbackCommand string
	}{
		{
			name:            "TUN MTU failure after tuntap add",
			plan:            issue236PartialTunPlan(),
			failCommand:     "ip link set dev podlaz0 mtu 1500",
			wantKind:        "tun-device",
			wantOwner:       netexecutor.OwnerTunDevice,
			rollbackCommand: "ip link del dev podlaz0",
		},
		{
			name:            "TUN up failure after tuntap add",
			plan:            issue236PartialTunPlan(),
			failCommand:     "ip link set dev podlaz0 up",
			wantKind:        "tun-device",
			wantOwner:       netexecutor.OwnerTunDevice,
			rollbackCommand: "ip link del dev podlaz0",
		},
		{
			name:            "route cache flush failure after route add",
			plan:            issue236PartialRoutePlan(),
			failCommand:     "ip -4 route flush cache",
			wantKind:        "route",
			wantOwner:       netexecutor.OwnerRoute,
			rollbackCommand: "ip -4 route del default dev podlaz0 table 51820",
		},
		{
			name:            "policy rule cache flush failure after rule add",
			plan:            issue236PartialRulePlan(),
			failCommand:     "ip -4 route flush cache",
			wantKind:        "policy-rule",
			wantOwner:       netexecutor.OwnerPolicyRule,
			rollbackCommand: "ip -4 rule del priority 10000 from all lookup 51820",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimeDir := t.TempDir()
			runner := &issue236PartialMutationRunner{failCommand: tt.failCommand}
			executor := newProductionTunPlanExecutor(runner)
			result, err := beginTunTransaction(context.Background(), runtimeDir, profile.Profile{ID: "example-profile"}, tt.plan, fixedClock())
			if err != nil {
				t.Fatalf("begin TUN transaction: %v", err)
			}

			err = applyVerifyTunTransactionDeferredRollback(context.Background(), result, executor)
			var mutationErr *tunNetworkMutationError
			if !errors.As(err, &mutationErr) {
				t.Fatalf("expected deferred network mutation error, got %v", err)
			}
			if !runner.hasOwnedResidue() {
				t.Fatal("fixture did not leave the expected post-mutation host state before transaction rollback")
			}

			tx, _, loadErr := result.Store.Load(result.TransactionID)
			if loadErr != nil {
				t.Fatalf("load failed transaction ownership: %v", loadErr)
			}
			if len(tx.AppliedSteps) != 1 {
				t.Fatalf("persisted applied_steps = %#v, want exactly one owned step", tx.AppliedSteps)
			}
			step := tx.AppliedSteps[0]
			if step.Kind != tt.wantKind || step.Owner != tt.wantOwner {
				t.Fatalf("persisted ownership = %#v, want kind=%s owner=%s", step, tt.wantKind, tt.wantOwner)
			}
			assertIssue236ExactRollbackMetadata(t, tx.Rollback, tt.wantKind)

			if rollbackErr := mutationErr.Rollback(context.Background(), executor); rollbackErr != nil {
				t.Fatalf("rollback partial production mutation: %v", rollbackErr)
			}
			if runner.hasOwnedResidue() {
				t.Fatalf("rollback left owned host residue: tun=%v route=%v rule=%v commands=%#v", runner.tunPresent, runner.routePresent, runner.rulePresent, runner.commands)
			}
			if got := runner.count(tt.rollbackCommand); got != 1 {
				t.Fatalf("exact rollback command %q count = %d, want 1; commands=%#v", tt.rollbackCommand, got, runner.commands)
			}
			summaries, warnings := transactionStatuses(runtimeDir)
			if len(summaries) != 0 || len(warnings) != 0 {
				t.Fatalf("successful rollback must remove transaction state: summaries=%#v warnings=%#v", summaries, warnings)
			}
		})
	}
}

func assertIssue236ExactRollbackMetadata(t *testing.T, rollback txstate.RollbackMetadata, wantKind string) {
	t.Helper()
	wantTUN, wantRoutes, wantRules := 0, 0, 0
	switch wantKind {
	case "tun-device":
		wantTUN = 1
	case "route":
		wantRoutes = 1
	case "policy-rule":
		wantRules = 1
	default:
		t.Fatalf("unsupported ownership kind %q", wantKind)
	}
	if len(rollback.TUN) != wantTUN || len(rollback.Routes) != wantRoutes || len(rollback.PolicyRules) != wantRules {
		t.Fatalf("rollback metadata is not exact for %s: %#v", wantKind, rollback)
	}
	if len(rollback.DNS) != 0 || len(rollback.NFTables) != 0 || len(rollback.GeneratedConfigs) != 0 || len(rollback.ChildProcesses) != 0 {
		t.Fatalf("rollback metadata contains unrelated ownership for %s: %#v", wantKind, rollback)
	}
}

func issue236PartialTunPlan() planner.TunPlan {
	plan := issue236BasePartialPlan()
	plan.TunDevice.Action = "create"
	return plan
}

func issue236PartialRoutePlan() planner.TunPlan {
	plan := issue236BasePartialPlan()
	plan.Routes = []planner.TunRoutePlan{{
		Family:      "ipv4",
		Destination: planner.IPv4DefaultRoute,
		Table:       planner.TunRoutingTable,
		Interface:   "podlaz0",
		Action:      "add",
		Reason:      "example default route",
	}}
	return plan
}

func issue236PartialRulePlan() planner.TunPlan {
	plan := issue236BasePartialPlan()
	plan.PolicyRules = []planner.TunPolicyRulePlan{{
		Family:   "ipv4",
		Priority: planner.TunRulePriority,
		Selector: planner.IPv4DefaultSelector,
		Table:    planner.TunRoutingTable,
		Action:   "add",
		Reason:   "example full-tunnel policy rule",
	}}
	return plan
}

func issue236BasePartialPlan() planner.TunPlan {
	return planner.TunPlan{
		Mode:        planner.ModeTun,
		ProfileID:   "example-profile",
		ProfileName: "Example Profile",
		TunDevice: planner.TunDevicePlan{
			Name:   "podlaz0",
			MTU:    1500,
			Action: "verify",
			Reason: "example TUN device",
		},
		DNS: planner.TunDNSPlan{
			Backend:    planner.DNSBackendSystemdResolved,
			TargetLink: "podlaz0",
			Servers:    []string{planner.DefaultTunDNSServer},
			Action:     planner.DNSActionConfigure,
			Reason:     "example per-link DNS",
		},
	}
}

type issue236PartialMutationRunner struct {
	failCommand  string
	failed       bool
	commands     []string
	tunPresent   bool
	routePresent bool
	rulePresent  bool
}

func (r *issue236PartialMutationRunner) Run(_ context.Context, name string, args ...string) (netexecutor.CommandResult, error) {
	command := strings.TrimSpace(name + " " + strings.Join(args, " "))
	r.commands = append(r.commands, command)
	if command == r.failCommand && !r.failed {
		r.failed = true
		return netexecutor.CommandResult{ExitCode: 1, Stderr: "injected post-mutation failure"}, errors.New("injected post-mutation failure")
	}

	switch command {
	case "ip -details link show dev podlaz0":
		return netexecutor.CommandResult{Stdout: productionTunLinkForTest}, nil
	case "ip -details -o link show dev podlaz0":
		return netexecutor.CommandResult{Stdout: productionTunOnelineLinkForTest}, nil
	case "ip tuntap add dev podlaz0 mode tun user podlaz-xray group podlaz-xray":
		r.tunPresent = true
	case "ip link set dev podlaz0 mtu 1500", "ip link set dev podlaz0 up":
	case "ip link del dev podlaz0":
		r.tunPresent = false
	case "ip -4 route add default dev podlaz0 table 51820":
		r.routePresent = true
	case "ip -4 route del default dev podlaz0 table 51820":
		r.routePresent = false
	case "ip -4 rule show priority 10000":
		return netexecutor.CommandResult{}, nil
	case "ip -4 rule add priority 10000 from all lookup 51820":
		r.rulePresent = true
	case "ip -4 rule del priority 10000 from all lookup 51820":
		r.rulePresent = false
	case "ip -4 route flush cache":
	default:
		return netexecutor.CommandResult{ExitCode: 2, Stderr: "unexpected command"}, fmt.Errorf("unexpected command: %s", command)
	}
	return netexecutor.CommandResult{}, nil
}

func (r *issue236PartialMutationRunner) hasOwnedResidue() bool {
	return r.tunPresent || r.routePresent || r.rulePresent
}

func (r *issue236PartialMutationRunner) count(command string) int {
	count := 0
	for _, got := range r.commands {
		if got == command {
			count++
		}
	}
	return count
}
