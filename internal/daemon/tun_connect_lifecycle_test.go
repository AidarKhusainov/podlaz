package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/profile"
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
				ServerRoute:    netsnapshot.Route{Status: netsnapshot.StatusMissing, Family: "ipv4", Destination: "vpn.example.test"},
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

func TestPlanAndDaemonRuntimeAgreeOnHostnameServerBypass(t *testing.T) {
	oldRouting := podlazRuntimeRoutingStaleResources
	podlazRuntimeRoutingStaleResources = func(context.Context) []netsnapshot.StaleResource { return nil }
	t.Cleanup(func() { podlazRuntimeRoutingStaleResources = oldRouting })

	p := profile.Profile{
		ID:               "hostname-profile",
		Name:             "Hostname Profile",
		Source:           profile.SourceImportedFile,
		Engine:           profile.EngineXray,
		Server:           "vpn.example.test",
		Port:             443,
		Protocol:         "vless",
		UserIdentity:     "00000000-0000-0000-0000-000000000702",
		Transport:        "tcp",
		Security:         "reality",
		Encryption:       "none",
		Flow:             "xtls-rprx-vision",
		ServerName:       "vpn.example.test",
		Fingerprint:      "chrome",
		RealityPublicKey: "public-key-parity",
		RealityShortID:   "abcd",
		RealitySpiderX:   "/",
	}
	snapshot := netsnapshot.FakeResolvedDesktop()
	snapshot.DefaultIPv4.Interface = "wlan0"
	snapshot.DefaultIPv4.Gateway = "192.0.2.1"
	snapshot.DefaultIPv4.Raw = "default via 192.0.2.1 dev wlan0 proto dhcp metric 600"
	snapshot.ServerRoute = netsnapshot.Route{
		Status:      netsnapshot.StatusDetected,
		Family:      "ipv4",
		Destination: "vpn.example.test",
		Interface:   "wlan0",
		Gateway:     "192.0.2.1",
		Raw:         "203.0.113.10 via 192.0.2.1 dev wlan0 src 192.0.2.55 uid 1000",
		Detail:      "server hostname vpn.example.test resolved to 203.0.113.10",
	}

	cliPlan, err := planner.PlanTun(p, snapshot)
	if err != nil {
		t.Fatalf("CLI-style TUN plan: %v", err)
	}
	if got := tunRuntimeServerAddress(cliPlan); got != "203.0.113.10" {
		t.Fatalf("CLI-style plan server bypass = %q, want 203.0.113.10", got)
	}

	manager := &XrayManager{RuntimeDir: t.TempDir()}
	prepared, err := manager.prepareTunHandoff(context.Background(), snapshot, api.HandoffBlock, netsnapshot.Options{Server: p.Server})
	if err != nil {
		t.Fatalf("daemon handoff preflight should accept same clean snapshot: %v", err)
	}
	daemonPlan, err := planner.PlanTun(p, prepared)
	if err != nil {
		t.Fatalf("daemon TUN plan: %v", err)
	}
	daemonPlan = xrayOwnedTunPlan(daemonPlan)
	if got := tunRuntimeServerAddress(daemonPlan); got != "203.0.113.10" {
		t.Fatalf("daemon plan server bypass = %q, want 203.0.113.10", got)
	}
	runtime, err := planTunCoreRuntime(p, "/run/podlaz/generated/xray.json", daemonPlan)
	if err != nil {
		t.Fatalf("daemon TUN runtime planning should not reject resolved hostname bypass: %v", err)
	}

	var config struct {
		Outbounds []struct {
			Settings struct {
				VNext []struct {
					Address string `json:"address"`
				} `json:"vnext"`
			} `json:"settings"`
			StreamSettings struct {
				RealitySettings struct {
					ServerName string `json:"serverName"`
				} `json:"realitySettings"`
			} `json:"streamSettings"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(runtime.XrayConfig, &config); err != nil {
		t.Fatalf("decode generated Xray config: %v", err)
	}
	if len(config.Outbounds) != 1 || len(config.Outbounds[0].Settings.VNext) != 1 {
		t.Fatalf("unexpected generated outbounds: %#v", config.Outbounds)
	}
	if config.Outbounds[0].Settings.VNext[0].Address != "203.0.113.10" {
		t.Fatalf("expected resolved IPv4 dial override, got %#v", config.Outbounds[0].Settings.VNext)
	}
	if config.Outbounds[0].StreamSettings.RealitySettings.ServerName != "vpn.example.test" {
		t.Fatalf("expected original hostname SNI/Reality serverName to be preserved, got %#v", config.Outbounds[0].StreamSettings.RealitySettings)
	}
	if p.Server != "vpn.example.test" {
		t.Fatalf("profile server was mutated: %q", p.Server)
	}
}
