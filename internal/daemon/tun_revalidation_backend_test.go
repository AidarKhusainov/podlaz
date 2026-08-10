package daemon

import (
	"testing"

	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestTunRevalidationServerAddressAcceptsDesiredPreExistingBypass(t *testing.T) {
	tx := txstate.Transaction{
		DesiredPlan: txstate.DesiredPlan{
			Routes: []txstate.RoutePlan{{
				Table: "main",
				CIDR:  "203.0.113.10/32",
				Dev:   "wlan0",
			}},
		},
	}
	got, err := tunRevalidationServerAddress(tx)
	if err != nil {
		t.Fatalf("resolve desired server bypass: %v", err)
	}
	if got != "203.0.113.10" {
		t.Fatalf("server=%q, want 203.0.113.10", got)
	}
}

func TestTunRevalidationServerAddressDeduplicatesDesiredAndOwnedBypass(t *testing.T) {
	tx := txstate.Transaction{
		DesiredPlan: txstate.DesiredPlan{Routes: []txstate.RoutePlan{{Table: "main", CIDR: "203.0.113.10/32", Dev: "wlan0"}}},
		Rollback:    txstate.RollbackMetadata{Routes: []txstate.RouteRollback{{Table: "main", CIDR: "203.0.113.10/32", Dev: "wlan0"}}},
	}
	got, err := tunRevalidationServerAddress(tx)
	if err != nil {
		t.Fatalf("resolve duplicate server bypass evidence: %v", err)
	}
	if got != "203.0.113.10" {
		t.Fatalf("server=%q, want 203.0.113.10", got)
	}
}

func TestTunRevalidationServerAddressRejectsAmbiguousBypass(t *testing.T) {
	tx := txstate.Transaction{
		DesiredPlan: txstate.DesiredPlan{Routes: []txstate.RoutePlan{
			{Table: "main", CIDR: "203.0.113.10/32", Dev: "wlan0"},
			{Table: "main", CIDR: "203.0.113.11/32", Dev: "wlan0"},
		}},
	}
	if _, err := tunRevalidationServerAddress(tx); err == nil {
		t.Fatal("expected ambiguous server bypass evidence to fail closed")
	}
}
