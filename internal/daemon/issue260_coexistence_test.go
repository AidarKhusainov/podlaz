package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestPrepareTunCoexistenceTreatsForeignNetworkingAsBaseline(t *testing.T) {
	m := NewXrayManager(t.TempDir())
	s := issue260ForeignBaseline()

	for _, handoff := range []string{api.HandoffBlock, api.HandoffStopKnown, api.HandoffReplacePodlaz} {
		got, err := m.prepareTunCoexistence(context.Background(), s, handoff, netsnapshot.Options{})
		if err != nil {
			t.Fatalf("prepareTunCoexistence(%q) blocked unrelated baseline: %v", handoff, err)
		}
		if len(got.TunDevices) != len(s.TunDevices) || len(got.PolicyRouting) != len(s.PolicyRouting) || len(got.DNS.ResolvedLinks) != len(s.DNS.ResolvedLinks) {
			t.Fatalf("prepareTunCoexistence(%q) mutated baseline projection: got=%#v want=%#v", handoff, got, s)
		}
	}
}

func TestPrepareTunCoexistenceAskRemainsMutationFreeAndUnsupported(t *testing.T) {
	m := NewXrayManager(t.TempDir())
	s := issue260ForeignBaseline()

	got, err := m.prepareTunCoexistence(context.Background(), s, api.HandoffAsk, netsnapshot.Options{})
	if err == nil {
		t.Fatal("non-interactive ask policy must be rejected")
	}
	if len(got.TunDevices) != len(s.TunDevices) || len(got.PolicyRouting) != len(s.PolicyRouting) || len(got.DNS.ResolvedLinks) != len(s.DNS.ResolvedLinks) {
		t.Fatalf("ask preflight mutated baseline projection: got=%#v want=%#v", got, s)
	}
}

func TestPrepareTunCoexistenceStillFailsClosedOnExactRecoveryState(t *testing.T) {
	m := NewXrayManager(t.TempDir())
	store := txstate.TransactionStore{RuntimeDir: m.runtimeDir(), Now: func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }}
	tx := txstate.NewTransaction("issue260-unfinished", "example-profile", planner.ModeTun, store.Now())
	if _, err := store.Save(tx); err != nil {
		t.Fatalf("save exact recovery fixture: %v", err)
	}
	if _, _, err := store.Transition(tx.ID, txstate.TransactionApplying); err != nil {
		t.Fatalf("mark exact recovery fixture applying: %v", err)
	}

	if _, err := m.prepareTunCoexistence(context.Background(), issue260ForeignBaseline(), api.HandoffBlock, netsnapshot.Options{}); err == nil {
		t.Fatal("exact unfinished Podlaz transaction must block a conflicting new mutation")
	}
}

func issue260ForeignBaseline() netsnapshot.Snapshot {
	s := netsnapshot.FakeDesktopWithServerRouteViaForeignVPN()
	s.DNS.ResolvedLinks = append(s.DNS.ResolvedLinks, netsnapshot.ResolvedLink{
		Index:            "9",
		Name:             "wg0",
		CurrentScopes:    []string{"DNS"},
		Protocols:        []string{"+DefaultRoute"},
		CurrentDNSServer: "198.51.100.53",
		DNSServers:       []string{"198.51.100.53"},
		DNSDomains:       []string{"~."},
	})
	rule := netsnapshot.PolicyRoutingSignal{Kind: "rule", Priority: "100", Selector: "from all", Table: "60000", Raw: "100: from all lookup 60000"}
	s.PolicyRouting = append(s.PolicyRouting, rule)
	s.IPv4PolicyRules.Rules = append(s.IPv4PolicyRules.Rules, rule)
	s.NetworkManager.ActiveConnections = []netsnapshot.NetworkManagerConnection{{
		Name:   "Existing tunnel",
		UUID:   "11111111-2222-3333-4444-555555555555",
		Type:   "vpn",
		Device: "wg0",
		State:  "activated",
	}}
	return s
}
