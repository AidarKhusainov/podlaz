package daemon

import (
	"context"
	"errors"
	"os/user"
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
	withCoreIdentityTestHooks(t, 0,
		func(string) (*user.User, error) {
			return &user.User{Uid: "997", Gid: "996", Username: "podlaz-xray"}, nil
		},
		func(string) (*user.Group, error) { return &user.Group{Gid: "996", Name: "podlaz-xray"}, nil },
	)

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

func TestConnectTunRecoversOwnedStaleAddressBeforeAuthoritativeConflictCheck(t *testing.T) {
	oldPreflight := preflightNativeTunSupport
	oldValidateDeps := validateTunRuntimeDependenciesHook
	oldRecover := automaticPodlazRecover
	t.Cleanup(func() {
		preflightNativeTunSupport = oldPreflight
		validateTunRuntimeDependenciesHook = oldValidateDeps
		automaticPodlazRecover = oldRecover
	})
	preflightNativeTunSupport = func(context.Context, string, coreExecutionIdentity) error { return nil }
	validateTunRuntimeDependenciesHook = func() error { return nil }
	withCoreIdentityTestHooks(t, 0,
		func(string) (*user.User, error) {
			return &user.User{Uid: "997", Gid: "996", Username: "podlaz-xray"}, nil
		},
		func(string) (*user.Group, error) { return &user.Group{Gid: "996", Name: "podlaz-xray"}, nil },
	)

	stale := netsnapshot.FakeDesktopWithStalepodlazResources()
	stale.DefaultIPv4.Interface = "wlan0"
	stale.DefaultIPv4.Gateway = "192.0.2.1"
	stale.ServerRoute = netsnapshot.Route{Status: netsnapshot.StatusDetected, Family: "ipv4", Destination: "vpn.example.test", Interface: "wlan0", Gateway: "192.0.2.1", Raw: "203.0.113.10 via 192.0.2.1 dev wlan0"}
	stale.IPv4Addresses = netsnapshot.IPAddressInventory{Inspection: netsnapshot.Finding{Status: netsnapshot.StatusDetected}, Addresses: []netsnapshot.IPAddress{{Family: "ipv4", Interface: "podlaz0", CIDR: planner.DefaultTunIPv4CIDR, Scope: "global"}}}
	stale.IPv4Routes = netsnapshot.RouteInventory{Inspection: netsnapshot.Finding{Status: netsnapshot.StatusDetected}, Routes: []netsnapshot.Route{{Status: netsnapshot.StatusDetected, Family: "ipv4", Destination: planner.DefaultTunIPv4CIDR, Interface: "podlaz0", Table: "local"}}}

	clean := netsnapshot.FakeResolvedDesktop()
	clean.DefaultIPv4.Interface = "wlan0"
	clean.DefaultIPv4.Gateway = "192.0.2.1"
	clean.ServerRoute = stale.ServerRoute
	clean.IPv4Addresses = netsnapshot.IPAddressInventory{Inspection: netsnapshot.Finding{Status: netsnapshot.StatusDetected}}
	clean.IPv4Routes = netsnapshot.RouteInventory{Inspection: netsnapshot.Finding{Status: netsnapshot.StatusDetected}}

	recoverCalls := 0
	automaticPodlazRecover = func(context.Context, string) error {
		recoverCalls++
		return nil
	}
	calls := 0
	manager := &XrayManager{
		RuntimeDir: t.TempDir(),
		XrayPath:   writeFakeXray(t, "#!/bin/sh\nexit 0\n"),
		snapshotCollector: func(context.Context, netsnapshot.Options) netsnapshot.Snapshot {
			calls++
			if calls <= 2 {
				return stale
			}
			return clean
		},
	}
	req := connectRequestForTest()
	req.Mode = planner.ModeTun
	req.Handoff = api.HandoffBlock

	_, err := manager.Connect(context.Background(), req)
	if recoverCalls != 1 {
		t.Fatalf("expected canonical recovery before final address preflight, calls=%d err=%v", recoverCalls, err)
	}
	if errors.Is(err, netexecutor.ErrTunAddressConflict) {
		t.Fatalf("valid owned stale address was blocked before recovery: %v", err)
	}
}
