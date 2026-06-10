package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AidarKhusainov/tunwarden/internal/api"
	"github.com/AidarKhusainov/tunwarden/internal/client"
	"github.com/AidarKhusainov/tunwarden/internal/recovery"
	txstate "github.com/AidarKhusainov/tunwarden/internal/state"
)

func TestServerExposesStatusOverUnixSocket(t *testing.T) {
	runtimeDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- (Server{RuntimeDir: runtimeDir}).Run(ctx) }()

	statusClient := client.StatusClient{SocketPath: runtimeDir + "/tunwardend.sock", Timeout: time.Second}
	var daemon string
	var service string
	for i := 0; i < 50; i++ {
		status, err := statusClient.Status(context.Background())
		if err == nil {
			daemon = status.Daemon
			service = status.Service
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("server shutdown failed: %v", err)
	}
	if daemon != "running" {
		t.Fatalf("expected daemon running status, got %q", daemon)
	}
	if service != api.ServiceManual {
		t.Fatalf("expected manual service status outside systemd, got %q", service)
	}
}

func TestDefaultStatusReportsSystemdServiceFromEnvironment(t *testing.T) {
	t.Setenv(api.ServiceEnv, api.ServiceSystemd)

	status := DefaultStatus(context.Background())

	if status.Service != api.ServiceSystemd {
		t.Fatalf("expected %q service, got %q", api.ServiceSystemd, status.Service)
	}
}

func TestServerExposesDoctorOverUnixSocket(t *testing.T) {
	runtimeDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- (Server{RuntimeDir: runtimeDir}).Run(ctx) }()

	doctorClient := client.DoctorClient{SocketPath: runtimeDir + "/tunwardend.sock", Timeout: time.Second}
	var source string
	var sawDaemonCheck bool
	for i := 0; i < 50; i++ {
		report, err := doctorClient.Doctor(context.Background())
		if err == nil {
			source = report.Source
			for _, check := range report.Checks {
				if check.Name == "daemon" && check.Severity == "OK" && check.Message == "running" {
					sawDaemonCheck = true
				}
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("server shutdown failed: %v", err)
	}
	if source != "daemon" {
		t.Fatalf("expected daemon doctor source, got %q", source)
	}
	if !sawDaemonCheck {
		t.Fatalf("expected daemon doctor check")
	}
}

func TestServerDoesNotStartupScanBeforeLock(t *testing.T) {
	runtimeDir := t.TempDir()
	lockPath := api.LockPath(runtimeDir)
	if err := os.WriteFile(lockPath, []byte("owned by another daemon"), 0o600); err != nil {
		t.Fatal(err)
	}
	var scanned bool

	err := (Server{
		RuntimeDir: runtimeDir,
		startupScan: func(context.Context) recovery.PlanResult {
			scanned = true
			return recovery.PlanResult{}
		},
	}).Run(context.Background())

	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected lock acquisition error, got %v", err)
	}
	if scanned {
		t.Fatal("startup scan must not run before daemon lock acquisition")
	}
}

func TestServerStartupScanReportsCleanState(t *testing.T) {
	runtimeDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- (Server{
			RuntimeDir: runtimeDir,
			startupScan: func(context.Context) recovery.PlanResult {
				return recovery.PlanResult{}
			},
		}).Run(ctx)
	}()

	status := waitForStatus(t, runtimeDir)
	report := waitForDoctor(t, runtimeDir)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("server shutdown failed: %v", err)
	}
	if status.StartupScan == nil {
		t.Fatal("expected startup scan in daemon status")
	}
	if status.StartupScan.Status != api.StartupScanStatusClean {
		t.Fatalf("expected clean startup scan, got %#v", status.StartupScan)
	}
	check := findDoctorCheck(report.Checks, "startup-recovery-scan")
	if check == nil {
		t.Fatalf("expected startup recovery doctor check, got %#v", report.Checks)
	}
	if check.Severity != "OK" || !strings.Contains(check.Message, "clean inactive state") {
		t.Fatalf("expected clean startup doctor check, got %#v", check)
	}
}

func TestServerStartupScanReportsStaleOwnedResource(t *testing.T) {
	runtimeDir := t.TempDir()
	generatedDir := filepath.Join(runtimeDir, "generated")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- (Server{
			RuntimeDir: runtimeDir,
			startupScan: func(context.Context) recovery.PlanResult {
				return recovery.PlanResult{Candidates: []recovery.Candidate{{Kind: "generated-runtime-configs", Description: "generated runtime configs", Target: generatedDir}}}
			},
		}).Run(ctx)
	}()

	status := waitForStatus(t, runtimeDir)
	report := waitForDoctor(t, runtimeDir)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("server shutdown failed: %v", err)
	}
	if status.StartupScan == nil || status.StartupScan.Status != api.StartupScanStatusStale {
		t.Fatalf("expected stale startup scan, got %#v", status.StartupScan)
	}
	if len(status.StartupScan.Candidates) != 1 || status.StartupScan.Candidates[0].Target != generatedDir {
		t.Fatalf("expected generated runtime startup candidate, got %#v", status.StartupScan.Candidates)
	}
	check := findDoctorCheck(report.Checks, "startup-recovery-scan")
	if check == nil || check.Severity != "WARN" || !strings.Contains(check.Message, "suggested action: tunwarden recover") {
		t.Fatalf("expected warning startup doctor check with recovery guidance, got %#v", check)
	}
}

func TestServerStartupScanReportsPendingTransaction(t *testing.T) {
	runtimeDir := t.TempDir()
	store := txstate.TransactionStore{RuntimeDir: runtimeDir}
	tx := txstate.NewTransaction("tx-startup", "profile-1", "tun", time.Now().UTC())
	tx.State = txstate.TransactionApplying
	tx.Rollback = txstate.RollbackMetadata{TUN: []txstate.TUNRollback{{InterfaceName: "tunwarden0", Owner: txstate.TransactionOwner}}}
	path, err := store.Save(tx)
	if err != nil {
		t.Fatalf("save transaction: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- (Server{RuntimeDir: runtimeDir}).Run(ctx) }()

	status := waitForStatus(t, runtimeDir)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("server shutdown failed: %v", err)
	}
	if status.StartupScan == nil {
		t.Fatal("expected startup scan in daemon status")
	}
	if status.StartupScan.Status != api.StartupScanStatusStale && status.StartupScan.Status != api.StartupScanStatusStaleIncomplete {
		t.Fatalf("expected stale startup scan, got %#v", status.StartupScan)
	}
	var found bool
	for _, candidate := range status.StartupScan.Candidates {
		if candidate.Transaction != nil && candidate.Transaction.ID == "tx-startup" && candidate.Transaction.Path == path {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected pending transaction startup candidate, got %#v", status.StartupScan.Candidates)
	}
}

func TestServerRefreshesStartupScanAfterRecoveryExecute(t *testing.T) {
	runtimeDir := t.TempDir()
	generatedDir := filepath.Join(runtimeDir, "generated")
	if err := os.MkdirAll(generatedDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- (Server{RuntimeDir: runtimeDir}).Run(ctx) }()

	before := waitForStatus(t, runtimeDir)
	if before.StartupScan == nil || len(before.StartupScan.Candidates) == 0 {
		cancel()
		<-done
		t.Fatalf("expected stale generated runtime startup candidate before recover, got %#v", before.StartupScan)
	}

	recoveryClient := client.RecoveryClient{SocketPath: filepath.Join(runtimeDir, api.SocketName), DialTimeout: time.Second, OperationTimeout: 5 * time.Second}
	result, err := recoveryClient.Recover(context.Background())
	if err != nil {
		cancel()
		<-done
		t.Fatalf("recover execute failed: %v", err)
	}
	var recoveredGenerated bool
	for _, item := range result.Results {
		if item.Candidate.Kind == "generated-runtime-configs" && item.Status == "recovered" {
			recoveredGenerated = true
			break
		}
	}
	if !recoveredGenerated {
		cancel()
		<-done
		t.Fatalf("expected generated runtime configs to be recovered, got %#v", result.Results)
	}

	after := waitForStatus(t, runtimeDir)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("server shutdown failed: %v", err)
	}
	if after.StartupScan == nil {
		t.Fatal("expected startup scan in daemon status after recovery")
	}
	for _, candidate := range after.StartupScan.Candidates {
		if candidate.Kind == "generated-runtime-configs" || candidate.Target == generatedDir {
			t.Fatalf("startup scan still reports recovered generated runtime state: %#v", after.StartupScan)
		}
	}
	if after.StartupScan.SuggestedAction == "tunwarden recover" {
		t.Fatalf("startup scan should not suggest recover after successful cleanup: %#v", after.StartupScan)
	}
}

func TestServerRejectsNonSocketAtSocketPath(t *testing.T) {
	runtimeDir := t.TempDir()
	socketPath := filepath.Join(runtimeDir, api.SocketName)
	if err := os.WriteFile(socketPath, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := (Server{RuntimeDir: runtimeDir}).Run(context.Background())
	if err == nil {
		t.Fatal("expected non-socket path to fail startup")
	}
	if !strings.Contains(err.Error(), "exists and is not a Unix socket") {
		t.Fatalf("expected explicit non-socket error, got %v", err)
	}
	if _, statErr := os.Stat(socketPath); statErr != nil {
		t.Fatalf("non-socket path must not be removed, stat error: %v", statErr)
	}
}

func waitForStatus(t *testing.T, runtimeDir string) api.StatusResponse {
	t.Helper()
	statusClient := client.StatusClient{SocketPath: filepath.Join(runtimeDir, api.SocketName), Timeout: time.Second}
	var lastErr error
	for i := 0; i < 50; i++ {
		status, err := statusClient.Status(context.Background())
		if err == nil {
			return status
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("status request did not succeed: %v", lastErr)
	return api.StatusResponse{}
}

func waitForDoctor(t *testing.T, runtimeDir string) api.DoctorResponse {
	t.Helper()
	doctorClient := client.DoctorClient{SocketPath: filepath.Join(runtimeDir, api.SocketName), Timeout: time.Second}
	var lastErr error
	for i := 0; i < 50; i++ {
		report, err := doctorClient.Doctor(context.Background())
		if err == nil {
			return report
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("doctor request did not succeed: %v", lastErr)
	return api.DoctorResponse{}
}

func findDoctorCheck(checks []api.DoctorCheck, name string) *api.DoctorCheck {
	for i := range checks {
		if checks[i].Name == name {
			return &checks[i]
		}
	}
	return nil
}
