package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func TestPreflightTunOwnershipAllowsCleanSnapshot(t *testing.T) {
	if err := preflightTunOwnership(netsnapshot.FakeResolvedDesktop(), api.HandoffBlock); err != nil {
		t.Fatalf("clean snapshot preflight failed: %v", err)
	}
}

func TestPreflightTunOwnershipBlocksForeignDefaultDNSOwner(t *testing.T) {
	err := preflightTunOwnership(netsnapshot.FakeDesktopWithForeignDefaultDNSOwner(), api.HandoffBlock)
	assertHandoffBlockerContains(t, err, "foreign route-only DNS owner", "wg0")
}

func TestPreflightTunOwnershipBlocksForeignTunLikeInterfaceWithoutDNSDomain(t *testing.T) {
	err := preflightTunOwnership(netsnapshot.FakeDesktopWithForeignTunLikeInterface(), api.HandoffBlock)
	assertHandoffBlockerContains(t, err, "foreign TUN-like interface", "wg0")
}

func TestPreflightTunOwnershipBlocksForeignPolicyRouting(t *testing.T) {
	err := preflightTunOwnership(netsnapshot.FakeDesktopWithForeignPolicyRouting(), api.HandoffBlock)
	assertHandoffBlockerContains(t, err, "foreign policy routing", "fwmark")
}

func TestPreflightTunOwnershipBlocksActiveNetworkManagerVPN(t *testing.T) {
	err := preflightTunOwnership(netsnapshot.FakeDesktopWithActiveNetworkManagerVPN(), api.HandoffBlock)
	assertHandoffBlockerContains(t, err, "active NetworkManager VPN", "Work VPN")
}

func TestPreflightTunOwnershipBlocksServerBypassViaForeignVPN(t *testing.T) {
	err := preflightTunOwnership(netsnapshot.FakeDesktopWithServerRouteViaForeignVPN(), api.HandoffBlock)
	assertHandoffBlockerContains(t, err, "VPN server route uses foreign VPN interface", "wg0")
}

func TestPrepareTunHandoffAskFailsBeforeMutation(t *testing.T) {
	manager := &XrayManager{RuntimeDir: t.TempDir()}
	_, err := manager.prepareTunHandoff(context.Background(), netsnapshot.FakeResolvedDesktop(), api.HandoffAsk, netsnapshot.Options{})
	assertHandoffBlockerContains(t, err, "--handoff=ask", "not supported")
}

func TestPrepareTunHandoffStopKnownUsesNetworkManagerDown(t *testing.T) {
	original := nmcliConnectionDown
	var stopped []string
	nmcliConnectionDown = func(_ context.Context, id string) error {
		stopped = append(stopped, id)
		return nil
	}
	t.Cleanup(func() { nmcliConnectionDown = original })

	manager := &XrayManager{RuntimeDir: t.TempDir()}
	calls := 0
	manager.snapshotCollector = func(context.Context, netsnapshot.Options) netsnapshot.Snapshot {
		calls++
		return netsnapshot.FakeResolvedDesktop()
	}
	_, err := manager.prepareTunHandoff(context.Background(), netsnapshot.FakeDesktopWithActiveNetworkManagerVPN(), api.HandoffStopKnown, netsnapshot.Options{})
	if err != nil {
		t.Fatalf("stop-known handoff failed: %v", err)
	}
	if len(stopped) != 1 || stopped[0] != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("unexpected stopped NM VPNs: %#v", stopped)
	}
	if calls != 1 {
		t.Fatalf("expected snapshot refresh after stop-known, got %d", calls)
	}
}

func TestPreflightTunOwnershipBlocksStalePodlazStateBeforeCreatePlan(t *testing.T) {
	snapshot := netsnapshot.FakeDesktopWithStalepodlazResources()
	plan, err := planner.PlanTun(profileFromSnapshot(connectRequestForTest().Profile), snapshot)
	if err != nil {
		t.Fatalf("fixture should still be plannable to expose the raw create risk: %v", err)
	}
	if plan.TunDevice.Action != "create" || plan.TunDevice.Name != netsnapshot.DefaultTunName {
		t.Fatalf("expected fixture to plan raw TUN create without preflight, got %#v", plan.TunDevice)
	}
	if plan.Firewall.TableAction != planner.FirewallActionValidate {
		t.Fatalf("expected fixture to expose stale nftables table validation risk, got %#v", plan.Firewall)
	}

	err = preflightTunOwnership(snapshot, api.HandoffBlock)
	if err == nil {
		t.Fatal("expected stale podlaz state blocker")
	}
	var blocker *tunStalePodlazStateBlocker
	if !errors.As(err, &blocker) {
		t.Fatalf("expected tunStalePodlazStateBlocker, got %T: %v", err, err)
	}
	body := err.Error()
	for _, want := range []string{"stale podlaz-owned networking state", "tun-device podlaz0", "nftables-table inet podlaz", "plz recover --execute --yes", "podlaz did not change network state"} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected stale blocker body to contain %q, got:\n%s", want, body)
		}
	}
}

func assertHandoffBlockerContains(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected handoff blocker")
	}
	var blocker *tunHandoffBlocker
	if !errors.As(err, &blocker) {
		t.Fatalf("expected tunHandoffBlocker, got %T: %v", err, err)
	}
	body := err.Error()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Fatalf("expected blocker to contain %q, got:\n%s", want, body)
		}
	}
}
