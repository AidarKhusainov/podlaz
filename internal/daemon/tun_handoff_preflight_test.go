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
	assertStringSetContains(t, blocker.Resources, "tun-device podlaz0", "nftables-table inet podlaz")
	body := blocker.Error()
	for _, want := range []string{"Unable to connect.", "recovery state that did not converge automatically", "podlaz debug recover"} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected product blocker body to contain %q, got:\n%s", want, body)
		}
	}
	for _, forbidden := range blocker.Resources {
		if strings.Contains(body, forbidden) {
			t.Fatalf("normal blocker leaked resource identity %q: %s", forbidden, body)
		}
	}
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
	assertStringSetContains(t, blocker.Resources, "policy-rule 9999")
	body := blocker.Error()
	for _, want := range []string{"Unable to connect.", "stale routing state it cannot safely prove it owns", "podlaz debug doctor --tun"} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected product blocker body to contain %q, got:\n%s", want, body)
		}
	}
	if strings.Contains(body, "9999") {
		t.Fatalf("normal blocker leaked historical routing identity: %s", body)
	}
}

func TestTunHandoffBlockerDefaultGuidanceIsProductNeutral(t *testing.T) {
	body := (&tunHandoffBlocker{Policy: "ask"}).Error()
	for _, forbidden := range []string{"NetworkManager VPN", "stop-known", "other VPN", "nmcli"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("handoff blocker guidance must remain product neutral; found %q in %q", forbidden, body)
		}
	}
}

func assertStringSetContains(t *testing.T, values []string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		found := false
		for _, value := range values {
			if value == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected values %#v to contain %q", values, want)
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

