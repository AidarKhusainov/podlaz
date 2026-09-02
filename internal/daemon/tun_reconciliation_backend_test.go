package daemon

import (
	"testing"

	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func authoritativeReconciliationSnapshot() netsnapshot.Snapshot {
	return netsnapshot.Snapshot{
		DefaultIPv4: netsnapshot.Route{Status: netsnapshot.StatusDetected, Interface: "wlan0", Gateway: "192.0.2.1"},
		ServerRoute: netsnapshot.Route{Status: netsnapshot.StatusDetected, Interface: "wlan0", Gateway: "192.0.2.1"},
		IPv4Addresses: netsnapshot.IPAddressInventory{
			Inspection: netsnapshot.Finding{Status: netsnapshot.StatusDetected},
			Addresses:  []netsnapshot.IPAddress{{Family: "ipv4", Interface: "wlan0", CIDR: "192.0.2.20/24", Scope: "global"}},
		},
		NetworkManager: netsnapshot.NetworkManager{
			Finding:                     netsnapshot.Finding{Status: netsnapshot.StatusDetected},
			ActiveConnectionsInspection: netsnapshot.Finding{Status: netsnapshot.StatusDetected},
		},
		DNS: netsnapshot.DNS{Resolved: netsnapshot.Finding{Status: netsnapshot.StatusDetected}},
	}
}

func TestMandatoryEvidenceMarksNetworkManagerInspectionUnknown(t *testing.T) {
	snapshot := authoritativeReconciliationSnapshot()
	snapshot.NetworkManager.ActiveConnectionsInspection = netsnapshot.Finding{Status: netsnapshot.StatusUnknown}

	evidence := tunMandatoryEvidenceFromSnapshot(snapshot)
	if evidence.NetworkManager != tunLocalProofUnknown {
		t.Fatalf("NetworkManager proof=%v, want unknown", evidence.NetworkManager)
	}
	if evidence.UplinkPath != tunLocalProofProven {
		t.Fatalf("uplink proof=%v, want proven", evidence.UplinkPath)
	}
}

func TestMandatoryEvidenceMarksResolvedInspectionUnknown(t *testing.T) {
	snapshot := authoritativeReconciliationSnapshot()
	snapshot.DNS.Resolved = netsnapshot.Finding{Status: netsnapshot.StatusUnknown}

	evidence := tunMandatoryEvidenceFromSnapshot(snapshot)
	if evidence.ResolvedDNS != tunLocalProofUnknown {
		t.Fatalf("resolved DNS proof=%v, want unknown", evidence.ResolvedDNS)
	}
}

func TestMandatoryEvidenceMarksTransientRouteGapUnknown(t *testing.T) {
	snapshot := authoritativeReconciliationSnapshot()
	snapshot.DefaultIPv4 = netsnapshot.Route{Status: netsnapshot.StatusUnknown}

	evidence := tunMandatoryEvidenceFromSnapshot(snapshot)
	if evidence.UplinkPath != tunLocalProofUnknown {
		t.Fatalf("uplink proof=%v, want unknown", evidence.UplinkPath)
	}
}
