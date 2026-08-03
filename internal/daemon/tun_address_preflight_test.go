package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestRequireTunAddressPreflightRejectsConflictAndIncompleteInspection(t *testing.T) {
	tests := []struct {
		name   string
		action string
		reason string
	}{
		{name: "foreign conflict", action: planner.TunAddressActionBlocked, reason: "198.18.0.1/32 is already assigned on eth1"},
		{name: "incomplete authoritative inspection", action: planner.TunAddressActionDaemonRecheck, reason: "IPv4 address or route inventory is incomplete"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := transactionPlanForTest()
			plan.TunAddress = unboundTunAddressPlanForTest()
			plan.TunAddress.Action = tt.action
			plan.TunAddress.Reason = tt.reason
			err := requireTunAddressPreflight(plan)
			if err == nil || !errors.Is(err, netexecutor.ErrTunAddressConflict) {
				t.Fatalf("expected stable TUN address conflict, got %v", err)
			}
			if got := tunLifecycleFailureClassification("preflight", err); got != "tun_address_conflict" {
				t.Fatalf("unexpected lifecycle classification: %q", got)
			}
		})
	}
}

func TestRequireTunAddressPreflightAcceptsDeterministicAssignment(t *testing.T) {
	plan := transactionPlanForTest()
	plan.TunAddress = unboundTunAddressPlanForTest()
	if err := requireTunAddressPreflight(plan); err != nil {
		t.Fatalf("expected clean deterministic address plan, got %v", err)
	}
}

func TestConnectTunAddressConflictStopsBeforeTransactionOrXrayMutation(t *testing.T) {
	oldPreflight := preflightNativeTunSupport
	oldValidateDeps := validateTunRuntimeDependenciesHook
	t.Cleanup(func() {
		preflightNativeTunSupport = oldPreflight
		validateTunRuntimeDependenciesHook = oldValidateDeps
	})
	preflightNativeTunSupport = func(context.Context, string, coreExecutionIdentity) error { return nil }
	validateTunRuntimeDependenciesHook = func() error { return nil }

	snapshot := netsnapshot.FakeResolvedDesktop()
	snapshot.DefaultIPv4.Interface = "wlan0"
	snapshot.DefaultIPv4.Gateway = "192.0.2.1"
	snapshot.ServerRoute = netsnapshot.Route{
		Status:      netsnapshot.StatusDetected,
		Family:      "ipv4",
		Destination: "vpn.example.test",
		Interface:   "wlan0",
		Gateway:     "192.0.2.1",
		Raw:         "203.0.113.10 via 192.0.2.1 dev wlan0",
	}
	snapshot.IPv4Addresses = netsnapshot.IPAddressInventory{
		Inspection: netsnapshot.Finding{Status: netsnapshot.StatusDetected},
		Addresses: []netsnapshot.IPAddress{{
			Family: "ipv4", Interface: "eth1", CIDR: planner.DefaultTunIPv4CIDR, Scope: "global",
		}},
	}
	snapshot.IPv4Routes = netsnapshot.RouteInventory{Inspection: netsnapshot.Finding{Status: netsnapshot.StatusDetected}}

	manager := &XrayManager{
		RuntimeDir: t.TempDir(),
		XrayPath:   writeFakeXray(t, "#!/bin/sh\nexit 99\n"),
		snapshotCollector: func(context.Context, netsnapshot.Options) netsnapshot.Snapshot {
			return snapshot
		},
	}
	req := connectRequestForTest()
	req.Mode = planner.ModeTun
	req.Handoff = api.HandoffBlock

	_, err := manager.Connect(context.Background(), req)
	if err == nil || !errors.Is(err, netexecutor.ErrTunAddressConflict) {
		t.Fatalf("expected pre-mutation TUN address conflict, got %v", err)
	}
	summaries, warnings := txstate.ScanTransactions(manager.RuntimeDir)
	if len(summaries) != 0 || len(warnings) != 0 {
		t.Fatalf("preflight conflict must leave no transaction state: summaries=%#v warnings=%#v", summaries, warnings)
	}
}
