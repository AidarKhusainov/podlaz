package daemon

import (
	"context"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestConnectTunServerBypassFailureRunsBeforeStopKnownHandoff(t *testing.T) {
	oldPreflight := preflightNativeTunSupport
	oldValidateDeps := validateTunRuntimeDependenciesHook
	oldNMDown := nmcliConnectionDown
	t.Cleanup(func() {
		preflightNativeTunSupport = oldPreflight
		validateTunRuntimeDependenciesHook = oldValidateDeps
		nmcliConnectionDown = oldNMDown
	})
	preflightNativeTunSupport = func(context.Context, string, coreExecutionIdentity) error { return nil }
	validateTunRuntimeDependenciesHook = func() error { return nil }

	var stoppedConnections []string
	nmcliConnectionDown = func(_ context.Context, id string) error {
		stoppedConnections = append(stoppedConnections, id)
		return nil
	}

	manager := &XrayManager{
		RuntimeDir: t.TempDir(),
		XrayPath:   writeFakeXray(t, "#!/bin/sh\nexit 0\n"),
		snapshotCollector: func(context.Context, netsnapshot.Options) netsnapshot.Snapshot {
			return netsnapshot.Snapshot{
				OS:             "linux",
				DefaultIPv4:    netsnapshot.Route{Status: netsnapshot.StatusDetected, Family: "ipv4", Destination: "default", Interface: "eth0", Gateway: "192.0.2.1"},
				ServerRoute:    netsnapshot.Route{Status: netsnapshot.StatusMissing, Family: "ipv4", Destination: "vpn.example"},
				DNS:            netsnapshot.DNS{Resolved: netsnapshot.Finding{Status: netsnapshot.StatusDetected, Summary: "systemd-resolved detected"}},
				NetworkManager: netsnapshot.NetworkManager{ActiveConnections: []netsnapshot.NetworkManagerConnection{{Name: "Example VPN", UUID: "11111111-1111-1111-1111-111111111111", Type: "vpn", Device: "tun9", State: "activated"}}},
				Nftables:       netsnapshot.Nftables{Availability: netsnapshot.Finding{Status: netsnapshot.StatusDetected, Summary: "nftables detected"}},
				IPv4:           netsnapshot.Finding{Status: netsnapshot.StatusDetected, Summary: "IPv4 detected"},
				IPv6:           netsnapshot.Finding{Status: netsnapshot.StatusDetected, Summary: "IPv6 detected"},
			}
		},
	}
	req := connectRequestForTest()
	req.Mode = planner.ModeTun
	req.Handoff = api.HandoffStopKnown

	_, err := manager.Connect(context.Background(), req)
	if err == nil {
		t.Fatal("expected missing server-bypass error")
	}
	if !isRuntimeUnavailableError(err) {
		t.Fatalf("expected runtime unavailable error, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "VPN server bypass") || !strings.Contains(err.Error(), "No network changes were applied.") {
		t.Fatalf("unexpected error text:\n%s", err)
	}
	if len(stoppedConnections) != 0 {
		t.Fatalf("server-bypass failure must happen before stop-known handoff, stopped=%v", stoppedConnections)
	}
	summaries, warnings := txstate.ScanTransactions(manager.RuntimeDir)
	if len(summaries) != 0 || len(warnings) != 0 {
		t.Fatalf("server-bypass failure must not leave transaction artifacts: summaries=%#v warnings=%#v", summaries, warnings)
	}
}
