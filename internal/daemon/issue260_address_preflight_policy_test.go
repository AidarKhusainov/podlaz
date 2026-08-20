package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func TestStopKnownDoesNotAuthorizeForeignTunAddressOverlap(t *testing.T) {
	s := netsnapshot.FakeResolvedDesktop()
	s.IPv4Addresses.Addresses = append(s.IPv4Addresses.Addresses, netsnapshot.IPAddress{
		Family: "ipv4", Interface: "tun9", CIDR: planner.DefaultTunIPv4CIDR, Scope: "global",
	})
	s.NetworkManager.ActiveConnections = []netsnapshot.NetworkManagerConnection{{
		Name: "Existing tunnel", UUID: "11111111-2222-3333-4444-555555555555", Type: "vpn", Device: "tun9", State: "activated",
	}}
	plan := planner.TunPlan{
		Snapshot:   s,
		TunAddress: planner.PlanTunAddress(s),
	}
	if plan.TunAddress.Action != planner.TunAddressActionBlocked {
		t.Fatalf("fixture must start with a concrete address collision, got %#v", plan.TunAddress)
	}

	m := NewXrayManager(t.TempDir())
	err := m.requireTunAddressPreflightBeforeHandoff(context.Background(), plan, api.HandoffStopKnown)
	if !errors.Is(err, netexecutor.ErrTunAddressConflict) {
		t.Fatalf("stop-known must not ignore a foreign address collision: %v", err)
	}
}
