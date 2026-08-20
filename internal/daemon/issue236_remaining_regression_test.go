package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestTunPlanningIgnoresCurrentScopesWithoutConfigurationEvidence(t *testing.T) {
	for _, currentScopes := range [][]string{{"none"}, {"DNS"}} {
		snapshot := netsnapshot.FakeResolvedDesktop()
		snapshot.DNS.ResolvedLinks = append(snapshot.DNS.ResolvedLinks, netsnapshot.ResolvedLink{
			Name:          "wg-example",
			CurrentScopes: currentScopes,
			DNSDomains:    []string{"~."},
		})

		if _, err := planner.PlanTunForSession(profileFromSnapshot(connectRequestForTest().Profile), snapshot, planner.TunOptions{}); err != nil {
			t.Fatalf("Current Scopes %v must not block a safe coexistence plan without conflicting critical evidence: %v", currentScopes, err)
		}
	}
}

func TestTunPlanningTreatsForeignRouteOnlyDNSAsBaselineAtAdmission(t *testing.T) {
	for _, currentScopes := range [][]string{{"none"}, {"DNS"}} {
		snapshot := netsnapshot.FakeResolvedDesktop()
		snapshot.DNS.ResolvedLinks = append(snapshot.DNS.ResolvedLinks, netsnapshot.ResolvedLink{
			Name:          "wg-example",
			CurrentScopes: currentScopes,
			Protocols:     []string{"+DefaultRoute"},
			DNSDomains:    []string{"~."},
		})

		if _, err := planner.PlanTunForSession(profileFromSnapshot(connectRequestForTest().Profile), snapshot, planner.TunOptions{}); err != nil {
			t.Fatalf("foreign route-only DNS state must remain baseline at plan admission for Current Scopes %v: %v", currentScopes, err)
		}
	}
}

func TestProductionTunTransactionRejectsDaemonCreatedTunBeforeMutation(t *testing.T) {
	t.Setenv(e2eTunHookGateEnv, "")
	t.Setenv(e2eTunHookPhaseEnv, "")

	runtimeDir := t.TempDir()
	runner := &issue236PartialMutationRunner{}
	executor := newProductionTunPlanExecutor(runner)
	result, err := beginTunTransaction(context.Background(), runtimeDir, profile.Profile{ID: "example-profile"}, issue236PartialTunPlan(), fixedClock())
	if err != nil {
		t.Fatalf("begin TUN transaction: %v", err)
	}

	err = applyVerifyTunTransactionDeferredRollback(context.Background(), result, executor)
	if err == nil || !strings.Contains(err.Error(), "daemon-created TUN links are unsupported") {
		t.Fatalf("daemon-created TUN path must fail before host mutation, got %v", err)
	}
	if runner.tunPresent || runner.count("ip tuntap add dev podlaz0 mode tun user podlaz-xray group podlaz-xray") != 0 {
		t.Fatalf("unsupported daemon-created path must not mutate host state: commands=%#v", runner.commands)
	}
	tx, _, loadErr := result.Store.Load(result.TransactionID)
	if loadErr != nil {
		t.Fatalf("load rejected transaction: %v", loadErr)
	}
	if len(tx.AppliedSteps) != 0 || len(tx.Rollback.TUN) != 0 {
		t.Fatalf("unsupported daemon-created path must not persist TUN ownership: applied=%#v rollback=%#v", tx.AppliedSteps, tx.Rollback)
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
			if len(tx.AppliedSteps) != 2 {
				t.Fatalf("persisted applied_steps = %#v, want address plus one owned network step", tx.AppliedSteps)
			}
			if tx.AppliedSteps[0].Kind != "tun-address" || tx.AppliedSteps[0].Owner != netexecutor.OwnerTunAddress {
				t.Fatalf("first ownership step must be bound TUN address, got %#v", tx.AppliedSteps)
			}
			step := tx.AppliedSteps[1]
			if step.Kind != tt.wantKind || step.Owner != tt.wantOwner {
				t.Fatalf("persisted ownership = %#v, want kind=%s owner=%s", step, tt.wantKind, tt.wantOwner)
			}
			assertIssue236ExactRollbackMetadata(t, tx.Rollback, tt.wantKind)

			if rollbackErr := mutationErr.Rollback(context.Background(), executor); rollbackErr != nil {
				t.Fatalf("rollback partial production mutation: %v", rollbackErr)
			}
			if runner.hasOwnedResidue() {
				t.Fatalf("rollback left owned host residue: address=%v tun=%v route=%v rule=%v commands=%#v", runner.addressPresent, runner.tunPresent, runner.routePresent, runner.rulePresent, runner.commands)
			}
			if got := runner.count(tt.rollbackCommand); got != 1 {
				t.Fatalf("exact rollback command %q count = %d, want 1; commands=%#v", tt.rollbackCommand, got, runner.commands)
			}
			if got := runner.count("ip -4 address del " + planner.DefaultTunIPv4CIDR + " dev podlaz0"); got != 1 {
				t.Fatalf("TUN address rollback command count = %d, want 1; commands=%#v", got, runner.commands)
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
	wantRoutes, wantRules := 0, 0
	switch wantKind {
	case "route":
		wantRoutes = 1
	case "policy-rule":
		wantRules = 1
	default:
		t.Fatalf("unsupported ownership kind %q", wantKind)
	}
	if len(rollback.TUN) != 0 || len(rollback.TUNAddresses) != 1 || len(rollback.Routes) != wantRoutes || len(rollback.PolicyRules) != wantRules {
		t.Fatalf("rollback metadata is not exact for address+%s: %#v", wantKind, rollback)
	}
	if len(rollback.DNS) != 0 || len(rollback.NFTables) != 0 || len(rollback.GeneratedConfigs) != 0 || len(rollback.ChildProcesses) != 0 {
		t.Fatalf("rollback metadata contains unrelated ownership for %s: %#v", wantKind, rollback)
	}
}

func issue236PartialTunPlan() planner.TunPlan {
	plan := issue236BasePartialPlan()
	plan.TunAddress = planner.TunAddressPlan{}
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
		TunAddress: planner.TunAddressPlan{
			Family:            "ipv4",
			Interface:         "podlaz0",
			CIDR:              planner.DefaultTunIPv4CIDR,
			Scope:             "global",
			Action:            planner.TunAddressActionAssign,
			Owner:             planner.TunAddressOwner,
			RollbackKey:       "podlaz0/" + planner.DefaultTunIPv4CIDR,
			LinkIndex:         7,
			LinkKind:          "tun",
			AppearedAfterCore: true,
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
	failCommand    string
	failed         bool
	commands       []string
	addressPresent bool
	tunPresent     bool
	routePresent   bool
	rulePresent    bool
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
		return netexecutor.CommandResult{Stdout: productionTunLinkOnelineForTest}, nil
	case "ip -4 -o address show dev podlaz0":
		if r.addressPresent {
			return netexecutor.CommandResult{Stdout: fmt.Sprintf("7: podlaz0    inet %s scope global podlaz0", planner.DefaultTunIPv4CIDR)}, nil
		}
		return netexecutor.CommandResult{}, nil
	case "ip tuntap add dev podlaz0 mode tun user podlaz-xray group podlaz-xray":
		r.tunPresent = true
	case "ip link set dev podlaz0 mtu 1500", "ip link set dev podlaz0 up":
	case "ip link del dev podlaz0":
		r.tunPresent = false
	case "ip -4 address replace " + planner.DefaultTunIPv4CIDR + " dev podlaz0":
		r.addressPresent = true
	case "ip -4 address del " + planner.DefaultTunIPv4CIDR + " dev podlaz0":
		r.addressPresent = false
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
	return r.addressPresent || r.tunPresent || r.routePresent || r.rulePresent
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
