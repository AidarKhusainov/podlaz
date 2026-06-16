package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AidarKhusainov/tunwarden/internal/api"
	netexecutor "github.com/AidarKhusainov/tunwarden/internal/network/executor"
	"github.com/AidarKhusainov/tunwarden/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/tunwarden/internal/network/snapshot"
	"github.com/AidarKhusainov/tunwarden/internal/profile"
)

func TestXrayManagerConnectTunWithFakeExecutorAndProcesses(t *testing.T) {
	dir := t.TempDir()
	runtimeDir := filepath.Join(dir, "runtime")
	fakeXrayArgs := filepath.Join(dir, "fake-xray.args")
	fakeAdapterArgs := filepath.Join(dir, "fake-tun2socks.args")
	fakeXray := writeDaemonFakeBinary(t, filepath.Join(dir, "fake-xray"), fakeXrayArgs)
	fakeAdapter := writeDaemonFakeBinary(t, filepath.Join(dir, "fake-tun2socks"), fakeAdapterArgs)
	t.Setenv(tunAdapterPathEnv, fakeAdapter)

	oldLookup := lookupTunRouteForProbe
	oldDial := dialTunProbeTarget
	oldResolve := resolveTunDNSName
	oldEUID := currentEUID
	t.Cleanup(func() {
		lookupTunRouteForProbe = oldLookup
		dialTunProbeTarget = oldDial
		resolveTunDNSName = oldResolve
		currentEUID = oldEUID
	})
	currentEUID = func() int { return 1000 }
	var routeProbes []string
	lookupTunRouteForProbe = func(ctx context.Context, host, tunDevice string) error {
		routeProbes = append(routeProbes, host+" via "+tunDevice)
		if tunDevice != netsnapshot.DefaultTunName {
			return fmt.Errorf("unexpected TUN device %q", tunDevice)
		}
		return nil
	}
	var tcpProbes []string
	dialTunProbeTarget = func(ctx context.Context, host string, port uint16) error {
		tcpProbes = append(tcpProbes, fmt.Sprintf("%s:%d", host, port))
		return nil
	}
	resolveTunDNSName = func(ctx context.Context, name string) (string, error) {
		if name != defaultTunDNSProbeName {
			return "", fmt.Errorf("unexpected DNS probe name %q", name)
		}
		return "93.184.216.34", nil
	}

	executor := &fakeTunPlanExecutor{}
	manager := &XrayManager{
		RuntimeDir:        runtimeDir,
		XrayPath:          fakeXray,
		StopTimeout:       200 * time.Millisecond,
		tunExecutor:       executor,
		snapshotCollector: fakeTunSnapshot,
	}

	p := daemonTunIntegrationProfile()
	resp, err := manager.Connect(context.Background(), api.ConnectRequest{Mode: planner.ModeTun, Profile: api.ProfileSnapshot{
		ID:               p.ID,
		Name:             p.Name,
		Source:           string(p.Source),
		Engine:           string(p.Engine),
		Server:           p.Server,
		Port:             p.Port,
		Protocol:         p.Protocol,
		UserIdentity:     p.UserIdentity,
		Transport:        p.Transport,
		Security:         p.Security,
		Encryption:       p.Encryption,
		Flow:             p.Flow,
		ServerName:       p.ServerName,
		RealityPublicKey: p.RealityPublicKey,
		RealityShortID:   p.RealityShortID,
		RealitySpiderX:   p.RealitySpiderX,
	}})
	if err != nil {
		t.Fatalf("connect TUN failed: %v", err)
	}
	if resp.Connection != "active" || resp.Mode != planner.ModeTun || !strings.Contains(resp.TUN, netsnapshot.DefaultTunName) {
		t.Fatalf("unexpected TUN lifecycle response: %#v", resp)
	}
	if executor.applyCalls != 1 || executor.verifyCalls != 1 || executor.rollbackCalls != 0 {
		t.Fatalf("unexpected fake executor counts after connect: %#v", executor)
	}
	if len(routeProbes) < 2 || len(tcpProbes) != 1 {
		t.Fatalf("expected route/TCP/DNS connectivity probes, route=%#v tcp=%#v", routeProbes, tcpProbes)
	}

	config, err := os.ReadFile(filepath.Join(runtimeDir, generatedDirName, generatedXrayName))
	if err != nil {
		t.Fatalf("expected generated TUN Xray config: %v", err)
	}
	var generated map[string]any
	if err := json.Unmarshal(config, &generated); err != nil {
		t.Fatalf("generated TUN Xray config is not valid JSON: %v", err)
	}
	configText := string(config)
	for _, want := range []string{p.Server, p.UserIdentity, p.Transport, p.Security, p.RealityPublicKey, p.RealityShortID} {
		if !strings.Contains(configText, want) {
			t.Fatalf("generated TUN Xray config does not contain expected field %q: %s", want, configText)
		}
	}

	xrayArgs, err := os.ReadFile(fakeXrayArgs)
	if err != nil {
		t.Fatalf("read fake Xray args: %v", err)
	}
	if got := strings.TrimSpace(string(xrayArgs)); !strings.Contains(got, "run") || !strings.Contains(got, "-config") {
		t.Fatalf("fake Xray received unexpected args: %q", got)
	}
	adapterArgs, err := os.ReadFile(fakeAdapterArgs)
	if err != nil {
		t.Fatalf("read fake tun2socks args: %v", err)
	}
	if got := strings.TrimSpace(string(adapterArgs)); !strings.Contains(got, "-device") || !strings.Contains(got, "tun://"+netsnapshot.DefaultTunName) || !strings.Contains(got, "-proxy") {
		t.Fatalf("fake tun2socks received unexpected args: %q", got)
	}

	status := manager.Status(context.Background())
	if status.Connection != "active" || status.Mode != planner.ModeTun || status.RuntimeConfigPath == "" {
		t.Fatalf("unexpected active status: %#v", status)
	}
	for _, secret := range []string{p.UserIdentity, p.RealityPublicKey, string(config)} {
		if strings.Contains(strings.Join(status.Warnings, "\n"), secret) {
			t.Fatalf("status warnings leaked secret/config content %q: %#v", secret, status.Warnings)
		}
	}

	disconnect, err := manager.Disconnect(context.Background())
	if err != nil {
		t.Fatalf("disconnect TUN failed: %v", err)
	}
	if disconnect.Connection != "inactive" || disconnect.TUN != "disabled" {
		t.Fatalf("unexpected disconnect response: %#v", disconnect)
	}
	if executor.rollbackCalls != 1 {
		t.Fatalf("expected one rollback on disconnect, got %#v", executor)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, generatedDirName, generatedXrayName)); !os.IsNotExist(err) {
		t.Fatalf("expected TUN generated config removed after disconnect, stat err=%v", err)
	}
}

func daemonTunIntegrationProfile() profile.Profile {
	return profile.Profile{
		ID:               "tun-vless",
		Name:             "TUN VLESS",
		Source:           profile.SourceImportedFile,
		Engine:           profile.EngineXray,
		Server:           "203.0.113.10",
		Port:             443,
		Protocol:         "vless",
		UserIdentity:     "00000000-0000-0000-0000-000000000701",
		Transport:        "tcp",
		Security:         "reality",
		Encryption:       "none",
		Flow:             "xtls-rprx-vision",
		ServerName:       "vpn.example",
		Fingerprint:      "chrome",
		RealityPublicKey: "public-key-tun",
		RealityShortID:   "abcd",
		RealitySpiderX:   "/",
	}
}

func fakeTunSnapshot(context.Context, netsnapshot.Options) netsnapshot.Snapshot {
	return netsnapshot.Snapshot{
		OS:          "linux",
		DefaultIPv4: netsnapshot.Route{Status: netsnapshot.StatusDetected, Family: "ipv4", Destination: "default", Interface: "eth0", Gateway: "192.0.2.1"},
		DefaultIPv6: netsnapshot.Route{Status: netsnapshot.StatusMissing, Family: "ipv6"},
		ServerRoute: netsnapshot.Route{Status: netsnapshot.StatusDetected, Family: "ipv4", Destination: "203.0.113.10", Interface: "eth0", Gateway: "192.0.2.1"},
		DNS:         netsnapshot.DNS{Mode: "systemd-resolved", Resolved: netsnapshot.Finding{Status: netsnapshot.StatusDetected, Summary: "systemd-resolved detected"}},
		Nftables:    netsnapshot.Nftables{Availability: netsnapshot.Finding{Status: netsnapshot.StatusDetected, Summary: "nft available"}, TunWardenTable: netsnapshot.Finding{Status: netsnapshot.StatusMissing, Summary: "table missing"}},
	}
}

type fakeTunPlanExecutor struct {
	applyCalls    int
	verifyCalls   int
	rollbackCalls int
}

func (f *fakeTunPlanExecutor) Apply(ctx context.Context, plan planner.TunPlan) ([]netexecutor.Step, error) {
	f.applyCalls++
	steps := []netexecutor.Step{{Kind: "tun-device", Target: plan.TunDevice.Name, Owner: netexecutor.OwnerTunDevice}}
	for _, route := range plan.Routes {
		if route.Action == "add" {
			steps = append(steps, netexecutor.Step{Kind: "route", Target: routeTarget(route), Owner: netexecutor.OwnerRoute})
		}
	}
	for _, rule := range plan.PolicyRules {
		if rule.Action == "add" {
			steps = append(steps, netexecutor.Step{Kind: "policy-rule", Target: policyRuleTarget(rule), Owner: netexecutor.OwnerPolicyRule})
		}
	}
	return steps, nil
}

func (f *fakeTunPlanExecutor) Verify(ctx context.Context, plan planner.TunPlan) error {
	f.verifyCalls++
	return nil
}

func (f *fakeTunPlanExecutor) Rollback(ctx context.Context, plan planner.TunPlan) error {
	f.rollbackCalls++
	return nil
}

func writeDaemonFakeBinary(t *testing.T, path, argsPath string) string {
	t.Helper()
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\nexec sleep 3600\n", argsPath)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake binary %s: %v", path, err)
	}
	return path
}
