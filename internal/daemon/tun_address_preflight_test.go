package daemon

import (
	"context"
	"errors"
	"os"
	"os/user"
	"testing"
	"time"

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
		{name: "selected address conflict", action: planner.TunAddressActionBlocked, reason: "selected session address is already assigned"},
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

func TestRequireTunAddressPreflightAcceptsSelectedAssignment(t *testing.T) {
	plan := transactionPlanForTest()
	plan.TunAddress = unboundTunAddressPlanForTest()
	if err := requireTunAddressPreflight(plan); err != nil {
		t.Fatalf("expected selected address plan, got %v", err)
	}
}

func TestPlanTunForSessionReallocatesUnrelatedAddressOverlap(t *testing.T) {
	s := netsnapshot.FakeResolvedDesktop()
	s.IPv4Addresses.Addresses = append(s.IPv4Addresses.Addresses, netsnapshot.IPAddress{
		Family: "ipv4", Interface: "eth1", CIDR: planner.DefaultTunIPv4CIDR, Scope: "global",
	})

	plan, err := planner.PlanTunForSession(testVLESSProfile(), s, planner.TunOptions{})
	if err != nil {
		t.Fatalf("unrelated baseline overlap must be reallocated, got %v", err)
	}
	if plan.TunAddress.CIDR == planner.DefaultTunIPv4CIDR {
		t.Fatalf("allocator reused unrelated occupied address: %#v", plan.TunAddress)
	}
	if err := requireTunAddressPreflight(plan); err != nil {
		t.Fatalf("reallocated exact address must pass daemon preflight: %v", err)
	}
}

func TestPlanTunForSessionFailsClosedOnIncompleteAddressInventory(t *testing.T) {
	s := netsnapshot.FakeResolvedDesktop()
	s.IPv4Addresses.Inspection.Status = netsnapshot.StatusUnknown
	if _, err := planner.PlanTunForSession(testVLESSProfile(), s, planner.TunOptions{}); err == nil {
		t.Fatal("incomplete authoritative address inventory must fail before mutation")
	}
}

func TestConnectTunRecoversExactTransactionBeforeFinalSessionAllocation(t *testing.T) {
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

	stale := netsnapshot.FakeResolvedDesktop()
	stale.IPv4Addresses.Addresses = append(stale.IPv4Addresses.Addresses, netsnapshot.IPAddress{
		Family: "ipv4", Interface: netsnapshot.DefaultTunName, CIDR: planner.DefaultTunIPv4CIDR, Scope: "global",
	})
	stale.TunDevices = []netsnapshot.TunDevice{{Name: netsnapshot.DefaultTunName, Status: netsnapshot.StatusDetected, Raw: "7: podlaz0: <POINTOPOINT,UP> mtu 1500 tun type tun"}}

	clean := netsnapshot.FakeResolvedDesktop()
	runtimeDir := t.TempDir()
	store := txstate.TransactionStore{RuntimeDir: runtimeDir, Now: func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }}
	tx := txstate.NewTransaction("tx-stale-address", "profile-1", planner.ModeTun, store.Now())
	path, err := store.Save(tx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Transition(tx.ID, txstate.TransactionApplying); err != nil {
		t.Fatal(err)
	}

	recoverCalls := 0
	automaticPodlazRecover = func(context.Context, string) error {
		recoverCalls++
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return nil
	}
	calls := 0
	manager := &XrayManager{
		RuntimeDir: runtimeDir,
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

	_, connectErr := manager.Connect(context.Background(), req)
	if recoverCalls != 1 {
		t.Fatalf("expected exact transaction recovery before final allocation, calls=%d err=%v", recoverCalls, connectErr)
	}
	if errors.Is(connectErr, netexecutor.ErrTunAddressConflict) {
		t.Fatalf("stale occupied historical address should be recovered/reallocated, not treated as a foreign conflict: %v", connectErr)
	}
}

func TestConnectTunIncompleteInventoryBlocksBeforeActiveReplaceDisconnect(t *testing.T) {
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

	s := netsnapshot.FakeResolvedDesktop()
	s.IPv4Addresses.Inspection.Status = netsnapshot.StatusUnknown
	manager := &XrayManager{
		RuntimeDir: t.TempDir(),
		XrayPath:   writeFakeXray(t, "#!/bin/sh\nexit 0\n"),
		snapshotCollector: func(context.Context, netsnapshot.Options) netsnapshot.Snapshot {
			return s
		},
	}
	manager.state = xrayState{Connection: "active", Mode: planner.ModeTun, ProfileID: "active-profile", TransactionID: "active-tx"}
	req := connectRequestForTest()
	req.Mode = planner.ModeTun
	req.Handoff = api.HandoffReplacePodlaz

	if _, err := manager.Connect(context.Background(), req); err == nil {
		t.Fatal("incomplete allocation evidence must fail before active replacement")
	}
	if manager.state.Connection != "active" || manager.state.TransactionID != "active-tx" {
		t.Fatalf("active session was changed before fail-closed allocation preflight: %#v", manager.state)
	}
}
