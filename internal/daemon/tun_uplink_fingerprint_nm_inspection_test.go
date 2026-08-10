package daemon

import (
	"testing"

	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func TestActiveNetworkManagerConnectionIDFailsClosedWhenConnectionInventoryIsUnknown(t *testing.T) {
	nm := netsnapshot.NetworkManager{
		Finding: netsnapshot.Finding{Status: netsnapshot.StatusDetected},
		ActiveConnectionsInspection: netsnapshot.Finding{
			Status: netsnapshot.StatusUnknown,
			Detail: "active connection inspection failed",
		},
	}
	if _, err := activeNetworkManagerConnectionID(nm, "wlan0"); err == nil {
		t.Fatal("expected unknown NetworkManager active-connection inspection to fail closed")
	}
}

func TestActiveNetworkManagerConnectionIDAcceptsAuthoritativeNoManagedUplink(t *testing.T) {
	nm := netsnapshot.NetworkManager{
		Finding: netsnapshot.Finding{Status: netsnapshot.StatusDetected},
		ActiveConnectionsInspection: netsnapshot.Finding{
			Status: netsnapshot.StatusDetected,
		},
	}
	got, err := activeNetworkManagerConnectionID(nm, "wlan0")
	if err != nil {
		t.Fatalf("authoritative empty active-connection inventory: %v", err)
	}
	if got != "" {
		t.Fatalf("NetworkManager ID=%q, want empty for unmanaged uplink", got)
	}
}
