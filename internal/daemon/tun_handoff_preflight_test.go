package daemon

import (
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
	if err == nil {
		t.Fatal("expected foreign DNS owner blocker")
	}
	var blocker *tunHandoffBlocker
	if !errors.As(err, &blocker) {
		t.Fatalf("expected tunHandoffBlocker, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "another VPN appears to own default DNS routing") || !strings.Contains(err.Error(), "wg0") {
		t.Fatalf("unexpected foreign DNS blocker body:\n%s", err.Error())
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
