package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestConnectTunActiveReplaceBlockedPlanDoesNotDisconnect(t *testing.T) {
	for _, tt := range []struct {
		name     string
		snapshot netsnapshot.Snapshot
		want     string
	}{
		{
			name:     "DNS blocked",
			snapshot: tunLifecycleSnapshotWithBlockedDNS(),
			want:     "DNS preflight blocked",
		},
		{
			name:     "firewall blocked",
			snapshot: tunLifecycleSnapshotWithBlockedFirewall(),
			want:     "firewall preflight blocked",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			installTunLifecyclePreflightTestHooks(t)
			manager, done, stopFile := activeTunManagerForDestructivePreflight(t, tt.snapshot)
			req := tunConnectRequestForLifecyclePreflight(api.HandoffReplacePodlaz)

			_, err := manager.Connect(context.Background(), req)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q before active replacement, got %v", tt.want, err)
			}
			assertLifecyclePreflightDidNotStopCore(t, done, stopFile)
		})
	}
}

func TestConnectTunStopKnownBlockedPlanDoesNotCallNmcliDown(t *testing.T) {
	for _, tt := range []struct {
		name     string
		snapshot netsnapshot.Snapshot
		want     string
	}{
		{
			name:     "DNS blocked",
			snapshot: tunLifecycleSnapshotWithActiveNMAndBlockedDNS(),
			want:     "DNS preflight blocked",
		},
		{
			name:     "firewall blocked",
			snapshot: tunLifecycleSnapshotWithActiveNMAndBlockedFirewall(),
			want:     "firewall preflight blocked",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			installTunLifecyclePreflightTestHooks(t)
			oldDown := nmcliConnectionDown
			downCalls := 0
			nmcliConnectionDown = func(context.Context, string) error {
				downCalls++
				return nil
			}
			t.Cleanup(func() { nmcliConnectionDown = oldDown })

			manager := &XrayManager{
				RuntimeDir: t.TempDir(),
				XrayPath:   writeFakeXray(t, lifecyclePreflightCoreScript()),
				snapshotCollector: func(context.Context, netsnapshot.Options) netsnapshot.Snapshot {
					return tt.snapshot
				},
			}
			_, err := manager.Connect(context.Background(), tunConnectRequestForLifecyclePreflight(api.HandoffStopKnown))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q before stop-known handoff, got %v", tt.want, err)
			}
			if downCalls != 0 {
				t.Fatalf("blocked plan called nmcli down %d time(s)", downCalls)
			}
		})
	}
}

func TestConnectTunActiveReplaceValidateOrReplaceReachesDisconnect(t *testing.T) {
	installTunLifecyclePreflightTestHooks(t)
	snapshot := tunLifecycleSnapshotWithExactActiveOwnedState()
	manager, done, stopFile := activeTunManagerForDestructivePreflight(t, snapshot)
	persistActiveOwnedTunTransactionForPreflight(
		t,
		manager.RuntimeDir,
		manager.state.TransactionID,
		manager.state.RuntimeConfigPath,
		snapshot,
	)

	_, err := manager.Connect(context.Background(), tunConnectRequestForLifecyclePreflight(api.HandoffReplacePodlaz))
	if err == nil {
		t.Fatal("expected later connect failure after active replacement reached disconnect boundary")
	}
	assertLifecyclePreflightStoppedCore(t, done, stopFile)
}

func installTunLifecyclePreflightTestHooks(t *testing.T) {
	t.Helper()
	oldEUID := currentEUID
	oldDeps := validateTunRuntimeDependenciesHook
	oldNative := preflightNativeTunSupport
	oldStale := podlazRuntimeRoutingStaleResources
	currentEUID = func() int { return 1000 }
	validateTunRuntimeDependenciesHook = func() error { return nil }
	preflightNativeTunSupport = func(context.Context, string, coreExecutionIdentity) error { return nil }
	podlazRuntimeRoutingStaleResources = func(context.Context) []netsnapshot.StaleResource { return nil }
	t.Cleanup(func() {
		currentEUID = oldEUID
		validateTunRuntimeDependenciesHook = oldDeps
		preflightNativeTunSupport = oldNative
		podlazRuntimeRoutingStaleResources = oldStale
	})
}

func tunConnectRequestForLifecyclePreflight(handoff string) api.ConnectRequest {
	req := connectRequestForTest()
	req.Mode = planner.ModeTun
	req.Handoff = handoff
	return req
}

func activeTunManagerForDestructivePreflight(t *testing.T, snapshot netsnapshot.Snapshot) (*XrayManager, <-chan struct{}, string) {
	t.Helper()
	runtimeDir := t.TempDir()
	configPath := filepath.Join(runtimeDir, generatedDirName, generatedXrayName)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatalf("create generated dir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`{"inbounds":[]}`), 0o600); err != nil {
		t.Fatalf("write generated config: %v", err)
	}

	stopFile := filepath.Join(runtimeDir, "stopped")
	corePath := writeFakeXray(t, lifecyclePreflightCoreScript())
	cmd := exec.Command(corePath)
	cmd.Env = append(os.Environ(), "STOP_FILE="+stopFile)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start active fake Xray: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		select {
		case <-done:
		default:
			_ = cmd.Process.Kill()
			<-done
		}
	})

	manager := &XrayManager{
		RuntimeDir:  runtimeDir,
		XrayPath:    corePath,
		StopTimeout: time.Second,
		tunExecutor: &handoffPreflightNoopTunExecutor{},
		snapshotCollector: func(context.Context, netsnapshot.Options) netsnapshot.Snapshot {
			return snapshot
		},
	}
	manager.mu.Lock()
	manager.cmd = cmd
	manager.done = done
	manager.state = xrayState{
		Connection:        "active",
		Mode:              planner.ModeTun,
		ProfileID:         "test-vless",
		ProfileName:       "test vless",
		RuntimeConfigPath: configPath,
		TransactionID:     "active-owned-tun",
	}
	manager.mu.Unlock()
	return manager, done, stopFile
}

func lifecyclePreflightCoreScript() string {
	return `#!/bin/sh
trap 'printf stop >> "$STOP_FILE"; exit 0' TERM
while true; do sleep 3600 & wait $!; done
`
}

func assertLifecyclePreflightDidNotStopCore(t *testing.T, done <-chan struct{}, stopFile string) {
	t.Helper()
	select {
	case <-done:
		t.Fatal("active core exited before blocked preflight returned")
	default:
	}
	if _, err := os.Stat(stopFile); err == nil {
		t.Fatal("blocked preflight stopped active core")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat stop marker: %v", err)
	}
}

func assertLifecyclePreflightStoppedCore(t *testing.T, done <-chan struct{}, stopFile string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("active replacement did not reach disconnect boundary")
	}
	if _, err := os.Stat(stopFile); err != nil {
		t.Fatalf("expected active core stop marker: %v", err)
	}
}

func tunLifecycleSnapshotWithBlockedDNS() netsnapshot.Snapshot {
	s := netsnapshot.FakeResolvedDesktop()
	s.DNS = netsnapshot.DNS{
		Mode:     "unknown",
		Resolved: netsnapshot.Finding{Status: netsnapshot.StatusMissing, Summary: "resolvectl not found"},
	}
	return s
}

func tunLifecycleSnapshotWithBlockedFirewall() netsnapshot.Snapshot {
	s := netsnapshot.FakeResolvedDesktop()
	s.Nftables.Availability = netsnapshot.Finding{Status: netsnapshot.StatusMissing, Summary: "nft not found"}
	s.Nftables.PodlazTable = netsnapshot.Finding{
		Status:  netsnapshot.StatusMissing,
		Summary: "podlaz nftables table not inspected because nft is unavailable",
	}
	return s
}

func tunLifecycleSnapshotWithActiveNMAndBlockedDNS() netsnapshot.Snapshot {
	s := netsnapshot.FakeDesktopWithActiveNetworkManagerVPN()
	s.DNS = netsnapshot.DNS{
		Mode:     "unknown",
		Resolved: netsnapshot.Finding{Status: netsnapshot.StatusMissing, Summary: "resolvectl not found"},
	}
	return s
}

func tunLifecycleSnapshotWithActiveNMAndBlockedFirewall() netsnapshot.Snapshot {
	s := netsnapshot.FakeDesktopWithActiveNetworkManagerVPN()
	s.Nftables.Availability = netsnapshot.Finding{Status: netsnapshot.StatusMissing, Summary: "nft not found"}
	s.Nftables.PodlazTable = netsnapshot.Finding{
		Status:  netsnapshot.StatusMissing,
		Summary: "podlaz nftables table not inspected because nft is unavailable",
	}
	return s
}

func tunLifecycleSnapshotWithExactActiveOwnedState() netsnapshot.Snapshot {
	s := netsnapshot.FakeResolvedDesktop()
	s.TunDevices = []netsnapshot.TunDevice{{
		Name:   netsnapshot.DefaultTunName,
		Status: netsnapshot.StatusDetected,
		Raw:    "7: podlaz0: <POINTOPOINT,NOARP,UP,LOWER_UP> mtu 1500 qdisc fq_codel state UNKNOWN mode DEFAULT group default qlen 500 tun type tun pi off vnet_hdr on persist off",
	}}
	s.IPv4Addresses.Inspection = netsnapshot.Finding{
		Status:  netsnapshot.StatusDetected,
		Summary: "IPv4 address inventory available",
	}
	s.IPv4Addresses.Addresses = []netsnapshot.IPAddress{{
		Family:    "ipv4",
		Interface: netsnapshot.DefaultTunName,
		CIDR:      planner.DefaultTunIPv4CIDR,
		Scope:     "global",
	}}
	s.IPv4Routes.Inspection = netsnapshot.Finding{
		Status:  netsnapshot.StatusDetected,
		Summary: "IPv4 route inventory available",
	}
	s.IPv4Routes.Routes = []netsnapshot.Route{{
		Family:      "ipv4",
		Interface:   netsnapshot.DefaultTunName,
		Destination: planner.DefaultTunIPv4CIDR,
		Table:       "local",
		Raw:         productionLocalTunRouteRawForTest,
	}}
	s.Nftables.PodlazTable = netsnapshot.Finding{
		Status:  netsnapshot.StatusDetected,
		Summary: "podlaz nftables table exists",
	}
	return s
}

func persistActiveOwnedTunTransactionForPreflight(t *testing.T, runtimeDir, transactionID, configPath string, activeSnapshot netsnapshot.Snapshot) {
	t.Helper()
	p := profileFromSnapshot(connectRequestForTest().Profile)
	clean := netsnapshot.FakeResolvedDesktop()
	plan, err := planner.PlanTun(p, clean)
	if err != nil {
		t.Fatalf("plan active-owned fixture from clean snapshot: %v", err)
	}
	plan.TunAddress.LinkIndex = 7
	plan.TunAddress.LinkKind = "tun"
	plan.TunAddress.AppearedAfterCore = true
	plan.TunAddress.Action = planner.TunAddressActionAssign
	store := txstate.TransactionStore{RuntimeDir: runtimeDir, Now: fixedClock()}
	tx := txstate.NewTransaction(transactionID, p.ID, planner.ModeTun, store.Now())
	tx.State = txstate.TransactionCommitted
	tx.DesiredPlan = desiredPlanFromTunPlan(plan)
	tx.DesiredPlan.Core.RuntimeConfigPath = configPath
	tx.Rollback = rollbackMetadataFromTunPlan(plan)
	tx.Rollback.GeneratedConfigs = append(tx.Rollback.GeneratedConfigs, txstate.GeneratedConfigRollback{
		Path:  configPath,
		Owner: txstate.TransactionOwner,
	})
	tx.AppliedSteps = appliedStepsFromRollbackMetadataForTest(tx.Rollback, store.Now())
	if len(tx.Rollback.TUNAddresses) != 1 || len(tx.Rollback.NFTables) != 1 {
		t.Fatalf("active-owned fixture must have durable address and nftables ownership: rollback=%#v active=%#v", tx.Rollback, activeSnapshot)
	}
	if _, err := store.Save(tx); err != nil {
		t.Fatalf("save active-owned transaction: %v", err)
	}
}

type handoffPreflightNoopTunExecutor struct{}

func (handoffPreflightNoopTunExecutor) Apply(context.Context, planner.TunPlan) ([]netexecutor.Step, error) {
	return nil, nil
}

func (handoffPreflightNoopTunExecutor) Verify(context.Context, planner.TunPlan) error { return nil }

func (handoffPreflightNoopTunExecutor) Rollback(context.Context, planner.TunPlan) error { return nil }
