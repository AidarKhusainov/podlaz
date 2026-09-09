package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestRecoverCapturedSameDaemonTerminalTunShapeConvergesWithoutReboot(t *testing.T) {
	runtimeDir := t.TempDir()
	configPath := filepath.Join(runtimeDir, generatedDirName, generatedXrayName)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"inbounds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	plan := transactionPlanForTest()
	plan.Firewall = terminalRecoveryFirewallPlan()
	store := txstate.TransactionStore{RuntimeDir: runtimeDir, Now: fixedClock()}
	tx := txstate.NewTransaction("tun-terminal-recovery", "profile-example", planner.ModeTun, store.Now())
	tx.State = txstate.TransactionCommitted
	tx.DesiredPlan = desiredPlanFromTunPlan(plan)
	tx.Rollback = rollbackMetadataFromTunPlan(plan)
	tx.Rollback.GeneratedConfigs = append(tx.Rollback.GeneratedConfigs, txstate.GeneratedConfigRollback{
		Path: configPath, Owner: txstate.TransactionOwner,
	})
	tx.AppliedSteps = appliedStepsFromRollbackMetadataForTest(tx.Rollback, store.Now())
	if _, err := txstate.MarkFailure(&tx, "synthetic earlier terminal rollback blocker", store.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(tx); err != nil {
		t.Fatal(err)
	}

	fakeXray := writeFakeXray(t, `#!/bin/sh
trap 'exit 0' TERM
while true; do sleep 3600 & wait $!; done
`)
	cmd := exec.Command(fakeXray)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	})

	rollback := &capturingTerminalRecoveryTunExecutor{}
	manager := &XrayManager{RuntimeDir: runtimeDir, StopTimeout: 2 * time.Second, tunExecutor: rollback}
	manager.mu.Lock()
	manager.cmd = cmd
	manager.done = done
	manager.state = xrayState{
		Connection:        "active",
		Mode:              planner.ModeTun,
		ProfileID:         "profile-example",
		ProfileName:       "Example profile",
		TUN:               "enabled (podlaz0)",
		RuntimeConfigPath: configPath,
		TransactionID:     tx.ID,
	}
	manager.mu.Unlock()

	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if err := continuation.Save(testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	protection := testArmedPrivacyProtection()
	if err := continuation.stateStore().SetProtection(&protection); err != nil {
		t.Fatal(err)
	}
	if err := continuation.disarm(networkSessionIntentDisconnect); err != nil {
		t.Fatal(err)
	}
	envelope := &privacyEnvelopeExecutorStub{exists: true}
	postNetworkCalls := 0
	continuation.continueTeardown = func(ctx context.Context, stateStore networkSessionStateStore) error {
		return continuePersistedNetworkSessionTeardownWith(ctx, stateStore, envelope, func(context.Context) error {
			postNetworkCalls++
			return nil
		})
	}

	sessionLifecycle := newNetworkSessionLifecycle(manager, continuation)
	gate := newNetworkSessionStartupMutationGate(sessionLifecycle)
	currentStatus := func(ctx context.Context) api.StatusResponse {
		status := manager.Status(ctx)
		if status.Connection == "active" {
			status.TunHealth = &api.TunHealthStatus{
				State: api.TunHealthCleanupRequired, NetworkGeneration: 1,
				Classification: api.TunHealthOwnershipInvalid,
			}
		}
		return status
	}
	runtime := &daemonRuntime{
		runtimeDir:              runtimeDir,
		lifecycle:               manager,
		authorizer:              AllowAuthorizer{},
		currentStatus:           currentStatus,
		operationLock:           newLifecycleOperationLock(),
		continuation:            continuation,
		sessionLifecycle:        sessionLifecycle,
		startupMutationGate:     gate,
		forceRefreshStartupScan: func(context.Context) {},
	}
	httpServer := (Server{}).newHTTPServer(runtime, newBootAutostartManifestStore(t.TempDir(), fixedBootID("boot-a")))

	response := executeRecoveryHTTPRequest(t, httpServer.Handler)
	if response.NetworkSession == nil || response.NetworkSession.LastResumeOutcome != api.NetworkSessionResumeOutcomeSucceeded || response.NetworkSession.NextAction != api.NetworkSessionRecoveryActionNone {
		t.Fatalf("same-daemon terminal recovery did not converge: %#v", response.NetworkSession)
	}
	if len(response.Warnings) != 0 {
		t.Fatalf("same-daemon terminal recovery warnings: %#v", response.Warnings)
	}
	if rollback.calls != 1 {
		t.Fatalf("host rollback calls=%d, want 1", rollback.calls)
	}
	if len(rollback.plan.Firewall.Chains) != 1 || len(rollback.plan.Firewall.Rules) != 1 {
		t.Fatalf("active recovery lost exact firewall composition: %#v", rollback.plan.Firewall)
	}
	if envelope.removeCalls != 1 || envelope.exists {
		t.Fatalf("Privacy Envelope did not converge: remove=%d exists=%v", envelope.removeCalls, envelope.exists)
	}
	if postNetworkCalls != 1 {
		t.Fatalf("post-Podlaz network verification calls=%d, want 1", postNetworkCalls)
	}
	if got := manager.Status(context.Background()); got.Connection != "inactive" {
		t.Fatalf("Xray manager remained non-terminal after recovery: %#v", got)
	}
	if _, _, err := store.Load(tx.ID); err == nil {
		t.Fatal("converged terminal recovery left transaction authority")
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("converged terminal recovery left generated config: %v", err)
	}
	if _, exists, err := continuation.stateStore().Load(); err != nil || exists {
		t.Fatalf("converged terminal recovery left Network Session authority: exists=%v err=%v", exists, err)
	}

	// A second request must prove clean/idempotent behavior without depending on
	// privileged nftables access from the Go test process. The production daemon
	// runs privileged; this fixture supplies authoritative read-only absence for
	// the three host inspectors used by an otherwise clean recovery scan.
	installCleanRecoveryCommandFixtures(t)
	second := executeRecoveryHTTPRequest(t, httpServer.Handler)
	if !networkSessionRecoveryConverged(second) {
		t.Fatalf("second recovery must be clean/idempotent: %#v", second)
	}
	if rollback.calls != 1 || envelope.removeCalls != 1 || postNetworkCalls != 1 {
		t.Fatalf("second recovery mutated network state: rollback=%d envelope=%d verify=%d", rollback.calls, envelope.removeCalls, postNetworkCalls)
	}
}

func installCleanRecoveryCommandFixtures(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	write := func(name, script string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatalf("write clean recovery %s fixture: %v", name, err)
		}
	}
	write("ip", `#!/bin/sh
echo 'Device "podlaz0" does not exist.' >&2
exit 1
`)
	write("nft", `#!/bin/sh
echo 'Error: No such file or directory' >&2
exit 1
`)
	write("resolvectl", `#!/bin/sh
echo 'Failed to resolve interface "podlaz0": No such device' >&2
exit 1
`)
	t.Setenv("PATH", dir)
}

func executeRecoveryHTTPRequest(t *testing.T, handler http.Handler) api.RecoveryResponse {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, api.RecoverPath, nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("recover status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	var response api.RecoveryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode recover response: %v", err)
	}
	return response
}

type capturingTerminalRecoveryTunExecutor struct {
	calls int
	plan  planner.TunPlan
}

func (e *capturingTerminalRecoveryTunExecutor) Apply(context.Context, planner.TunPlan) ([]netexecutor.Step, error) {
	return nil, nil
}

func (e *capturingTerminalRecoveryTunExecutor) Verify(context.Context, planner.TunPlan) error {
	return nil
}

func (e *capturingTerminalRecoveryTunExecutor) Rollback(_ context.Context, plan planner.TunPlan) error {
	e.calls++
	e.plan = plan
	return nil
}

func terminalRecoveryFirewallPlan() planner.TunFirewallPlan {
	return planner.TunFirewallPlan{
		Backend:     planner.FirewallBackendNftables,
		Family:      "inet",
		Table:       "podlaz",
		TableAction: planner.FirewallTableAction,
		Chains: []planner.TunFirewallChainPlan{{
			Name:     planner.FirewallOutputChain,
			Type:     planner.FirewallChainTypeFilter,
			Hook:     planner.FirewallOutputHook,
			Priority: planner.FirewallOutputPriority,
			Policy:   planner.FirewallDefaultChainPolicy,
			Action:   planner.FirewallTableAction,
		}},
		Rules: []planner.TunFirewallRulePlan{{
			Chain:       planner.FirewallOutputChain,
			Expr:        `oifname != "podlaz0"`,
			Verdict:     planner.FirewallVerdictReject,
			Action:      planner.FirewallActionAdd,
			Ownership:   planner.FirewallKillSwitchOwner,
			RollbackKey: planner.FirewallKillSwitchKey,
		}},
	}
}
