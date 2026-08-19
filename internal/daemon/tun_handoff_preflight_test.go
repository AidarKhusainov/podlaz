package daemon

import (
	"errors"
	"strings"
	"testing"

	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func TestStalePodlazStateBlockerKeepsExactPodlazResidueActionable(t *testing.T) {
	snapshot := netsnapshot.FakeDesktopWithStalepodlazResources()
	blocker := stalePodlazStateBlocker(snapshot)
	if blocker == nil {
		t.Fatal("expected exact Podlaz-looking stale resources to remain actionable for diagnostics/recovery")
	}
	assertStalePodlazBlockerContains(t, blocker, "tun-device podlaz0", "nftables-table inet podlaz")
}

func TestStalePodlazStateBlockerDoesNotGrantRoutingAuthorityFromHistoricalShape(t *testing.T) {
	snapshot := netsnapshot.FakeResolvedDesktop()
	snapshot.PolicyRouting = []netsnapshot.PolicyRoutingSignal{{
		Kind:     "rule",
		Priority: podlazServerRulePriority,
		Selector: "to 198.51.100.10",
		Table:    "main",
		Raw:      "9999: to 198.51.100.10 lookup main",
	}}
	blocker := stalePodlazStateBlocker(snapshot)
	if blocker == nil {
		t.Fatal("historical-looking routing residue should remain diagnostically actionable")
	}
	if blocker.RoutingRecoveryAvailable {
		t.Fatal("historical priority resemblance must not grant cleanup authority")
	}
	assertStalePodlazBlockerContains(t, blocker, "ambiguous stale routing state", "policy-rule 9999")
}

func TestTunHandoffBlockerDefaultGuidanceIsProductNeutral(t *testing.T) {
	body := (&tunHandoffBlocker{Policy: "ask"}).Error()
	for _, forbidden := range []string{"NetworkManager VPN", "stop-known", "other VPN", "nmcli"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("handoff blocker guidance must remain product neutral; found %q in %q", forbidden, body)
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

func assertStalePodlazBlockerContains(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected stale podlaz state blocker")
	}
	var blocker *tunStalePodlazStateBlocker
	if !errors.As(err, &blocker) {
		t.Fatalf("expected tunStalePodlazStateBlocker, got %T: %v", err, err)
	}
	body := err.Error()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Fatalf("expected stale blocker body to contain %q, got:\n%s", want, body)
		}
	}
}
