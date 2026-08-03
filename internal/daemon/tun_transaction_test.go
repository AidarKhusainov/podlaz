package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestTunTransactionWaitsForExplicitCommitAfterApplyAndVerify(t *testing.T) {
	runtimeDir := t.TempDir()
	executor := &recordingTunExecutor{}
	result, err := runTunTransaction(context.Background(), runtimeDir, profile.Profile{ID: "test-profile"}, transactionPlanForTest(), executor, fixedClock())
	if err != nil {
		t.Fatalf("run TUN transaction failed: %v", err)
	}
	tx, _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Load(result.TransactionID)
	if err != nil {
		t.Fatalf("load transaction: %v", err)
	}
	if tx.State != txstate.TransactionVerifying {
		t.Fatalf("expected verifying before core verification, got %s", tx.State)
	}
	if strings.Join(executor.calls, ",") != "apply,verify" {
		t.Fatalf("unexpected executor calls: %#v", executor.calls)
	}
	if err := commitTunTransaction(result.Store, result.TransactionID); err != nil {
		t.Fatalf("commit transaction: %v", err)
	}
	tx, _, err = (txstate.TransactionStore{RuntimeDir: runtimeDir}).Load(result.TransactionID)
	if err != nil {
		t.Fatalf("reload transaction: %v", err)
	}
	if tx.State != txstate.TransactionCommitted {
		t.Fatalf("expected committed transaction after explicit commit, got %s", tx.State)
	}
}

func TestTunTransactionDoesNotPersistPreApplyRollbackOwnership(t *testing.T) {
	runtimeDir := t.TempDir()
	executor := &preApplyInspectingTunExecutor{t: t, runtimeDir: runtimeDir}
	_, err := runTunTransaction(context.Background(), runtimeDir, profile.Profile{ID: "test-profile"}, transactionPlanWithServerBypassForTest(), executor, fixedClock())
	if err != nil {
		t.Fatalf("run TUN transaction failed: %v", err)
	}
	if !executor.inspected {
		t.Fatal("expected executor to inspect the crash-window transaction state")
	}
}

func TestTunTransactionRecordsGeneratedConfigRollbackBeforePreflight(t *testing.T) {
	runtimeDir := t.TempDir()
	clock := fixedClock()
	result, err := beginTunTransaction(context.Background(), runtimeDir, profile.Profile{ID: "test-profile"}, transactionPlanForTest(), clock)
	if err != nil {
		t.Fatalf("begin TUN transaction: %v", err)
	}
	runtimeConfigPath := "/run/podlaz/generated/xray.json"
	if err := saveGeneratedConfigRollbackMetadata(result.Store, result.TransactionID, runtimeConfigPath, clock()); err != nil {
		t.Fatalf("save generated config rollback metadata: %v", err)
	}
	tx, _, err := result.Store.Load(result.TransactionID)
	if err != nil {
		t.Fatalf("load transaction: %v", err)
	}
	if tx.DesiredPlan.Core.RuntimeConfigPath != runtimeConfigPath || tx.DesiredPlan.Core.ProcessLabel != "xray" {
		t.Fatalf("expected core desired plan before preflight, got %#v", tx.DesiredPlan.Core)
	}
	if len(tx.Rollback.GeneratedConfigs) != 1 || tx.Rollback.GeneratedConfigs[0].Path != runtimeConfigPath {
		t.Fatalf("expected generated config rollback metadata before preflight, got %#v", tx.Rollback.GeneratedConfigs)
	}
	if tx.Health.Status != "core-preflight-planned" {
		t.Fatalf("expected preflight health marker, got %#v", tx.Health)
	}
}

func TestRollbackTunTransactionStopsChildProcessesAfterExecutorRollback(t *testing.T) {
	runtimeDir := t.TempDir()
	clock := fixedClock()
	store := txstate.TransactionStore{RuntimeDir: runtimeDir, Now: clock}
	tx := txstate.NewTransaction("tun-order-test", "test-profile", planner.ModeTun, clock())
	tx.Rollback.ChildProcesses = []txstate.ChildProcessRollback{{PID: 12345, Label: "xray", Owner: txstate.TransactionOwner}}
	if _, err := store.Save(tx); err != nil {
		t.Fatalf("save transaction: %v", err)
	}

	var order []string
	oldStop := stopRollbackChildProcesses
	stopRollbackChildProcesses = func(txstate.Transaction) error {
		order = append(order, "stop-child")
		return nil
	}
	defer func() { stopRollbackChildProcesses = oldStop }()

	executor := &rollbackOrderExecutor{order: &order}
	if err := rollbackTunTransaction(context.Background(), store, &tx, transactionPlanForTest(), executor); err != nil {
		t.Fatalf("rollback TUN transaction: %v", err)
	}
	if strings.Join(order, ",") != "executor-rollback,stop-child" {
		t.Fatalf("expected host rollback before child stop, got %#v", order)
	}
}

func TestRollbackTunTransactionPreservesChildAndRecoveryMetadataWhenHostRollbackFails(t *testing.T) {
	runtimeDir := t.TempDir()
	clock := fixedClock()
	store := txstate.TransactionStore{RuntimeDir: runtimeDir, Now: clock}
	tx := txstate.NewTransaction("tun-host-rollback-failure", "test-profile", planner.ModeTun, clock())
	configPath := runtimeDir + "/generated/xray.json"
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatalf("create generated config directory: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write generated config: %v", err)
	}
	tx.Rollback.GeneratedConfigs = []txstate.GeneratedConfigRollback{{Path: configPath, Owner: txstate.TransactionOwner}}
	tx.Rollback.ChildProcesses = []txstate.ChildProcessRollback{{PID: 12345, Label: "xray", ConfigRef: configPath, Owner: txstate.TransactionOwner}}
	if _, err := store.Save(tx); err != nil {
		t.Fatalf("save transaction: %v", err)
	}

	hostErr := errors.New("address identity could not be revalidated")
	stopCalls := 0
	err := rollbackPreparedTunFailureWithChildStopper(
		context.Background(),
		store,
		&tx,
		transactionPlanForTest(),
		&failingRollbackTunExecutor{err: hostErr},
		func(txstate.Transaction) error {
			stopCalls++
			return nil
		},
	)
	if !errors.Is(err, hostErr) {
		t.Fatalf("expected host rollback failure, got %v", err)
	}
	if stopCalls != 0 {
		t.Fatalf("Xray child must remain running while host cleanup is unproven, stop calls=%d", stopCalls)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("recovery identity config must remain after failed host rollback: %v", err)
	}
	loaded, _, err := store.Load(tx.ID)
	if err != nil {
		t.Fatalf("load failed transaction: %v", err)
	}
	if loaded.State != txstate.TransactionFailed || !loaded.Rollback.Available() {
		t.Fatalf("failed host rollback must preserve recoverable transaction, got %#v", loaded)
	}
}

func TestTunTransactionRollsBackOnlyAppliedStepsAfterPartialApplyFailure(t *testing.T) {
	runtimeDir := t.TempDir()
	executor := &recordingTunExecutor{applyErr: errors.New("route apply failed")}
	_, err := runTunTransaction(context.Background(), runtimeDir, profile.Profile{ID: "test-profile"}, transactionPlanForTest(), executor, fixedClock())
	if err == nil || !strings.Contains(err.Error(), "rolled back applied") {
		t.Fatalf("expected rolled back apply failure, got %v", err)
	}
	summaries, warnings := txstate.ScanTransactions(runtimeDir)
	if len(warnings) > 0 || len(summaries) != 0 {
		t.Fatalf("successful failed-connect rollback must remove or neutralize transaction file, summaries=%#v warnings=%#v", summaries, warnings)
	}
	if strings.Join(executor.calls, ",") != "apply,rollback" {
		t.Fatalf("unexpected executor calls: %#v", executor.calls)
	}
}

func TestTunTransactionRollsBackVerifyFailure(t *testing.T) {
	runtimeDir := t.TempDir()
	executor := &recordingTunExecutor{verifyErr: errors.New("route missing")}
	_, err := runTunTransaction(context.Background(), runtimeDir, profile.Profile{ID: "test-profile"}, transactionPlanForTest(), executor, fixedClock())
	if err == nil {
		t.Fatal("expected verify failure")
	}
	summaries, warnings := txstate.ScanTransactions(runtimeDir)
	if len(warnings) > 0 || len(summaries) != 0 {
		t.Fatalf("successful failed-connect rollback must remove or neutralize transaction file, summaries=%#v warnings=%#v", summaries, warnings)
	}
	if strings.Join(executor.calls, ",") != "apply,verify,rollback" {
		t.Fatalf("unexpected executor calls: %#v", executor.calls)
	}
}

type preApplyInspectingTunExecutor struct {
	t          *testing.T
	runtimeDir string
	inspected  bool
}

func (e *preApplyInspectingTunExecutor) Apply(context.Context, planner.TunPlan) ([]netexecutor.Step, error) {
	e.t.Helper()
	e.inspected = true
	summaries, warnings := txstate.ScanTransactions(e.runtimeDir)
	if len(warnings) > 0 || len(summaries) != 1 {
		e.t.Fatalf("unexpected transaction scan during apply: summaries=%#v warnings=%#v", summaries, warnings)
	}
	if summaries[0].State != txstate.TransactionApplying || !summaries[0].RequiresCleanup {
		e.t.Fatalf("expected applying transaction during apply, got %#v", summaries[0])
	}
	tx, _, err := (txstate.TransactionStore{RuntimeDir: e.runtimeDir}).Load(summaries[0].ID)
	if err != nil {
		e.t.Fatalf("load transaction during apply: %v", err)
	}
	if len(tx.Rollback.Routes) != 0 || len(tx.Rollback.PolicyRules) != 0 || len(tx.Rollback.TUN) != 0 || len(tx.Rollback.DNS) != 0 || len(tx.Rollback.NFTables) != 0 {
		e.t.Fatalf("pre-apply transaction must not claim rollback ownership, got %#v", tx.Rollback)
	}
	return nil, nil
}

func (e *preApplyInspectingTunExecutor) Verify(context.Context, planner.TunPlan) error { return nil }

func (e *preApplyInspectingTunExecutor) Rollback(context.Context, planner.TunPlan) error { return nil }

type recordingTunExecutor struct {
	applyErr      error
	verifyErr     error
	calls         []string
	lastApplyPlan planner.TunPlan
}

func (e *recordingTunExecutor) Apply(_ context.Context, plan planner.TunPlan) ([]netexecutor.Step, error) {
	e.calls = append(e.calls, "apply")
	e.lastApplyPlan = plan
	if e.applyErr != nil {
		return []netexecutor.Step{{Kind: "tun-device", Target: "podlaz0", Owner: netexecutor.OwnerTunDevice}}, e.applyErr
	}
	return []netexecutor.Step{
		{Kind: "tun-device", Target: "podlaz0", Owner: netexecutor.OwnerTunDevice},
		{Kind: "route", Target: "podlaz default", Owner: netexecutor.OwnerRoute},
		{Kind: "policy-rule", Target: policyRuleTarget(plan.PolicyRules[0]), Owner: netexecutor.OwnerPolicyRule},
	}, nil
}

func unboundTunAddressPlanForTest() planner.TunAddressPlan {
	return planner.TunAddressPlan{
		Family:      "ipv4",
		Interface:   "podlaz0",
		CIDR:        planner.DefaultTunIPv4CIDR,
		Scope:       "global",
		Action:      planner.TunAddressActionAssign,
		Owner:       planner.TunAddressOwner,
		RollbackKey: "podlaz0/" + planner.DefaultTunIPv4CIDR,
	}
}

func (e *recordingTunExecutor) Verify(context.Context, planner.TunPlan) error {
	e.calls = append(e.calls, "verify")
	return e.verifyErr
}

func (e *recordingTunExecutor) Rollback(context.Context, planner.TunPlan) error {
	e.calls = append(e.calls, "rollback")
	return nil
}

type failingRollbackTunExecutor struct {
	err error
}

func (e *failingRollbackTunExecutor) Apply(context.Context, planner.TunPlan) ([]netexecutor.Step, error) {
	return nil, nil
}

func (e *failingRollbackTunExecutor) Verify(context.Context, planner.TunPlan) error { return nil }

func (e *failingRollbackTunExecutor) Rollback(context.Context, planner.TunPlan) error {
	return e.err
}

type rollbackOrderExecutor struct {
	order *[]string
}

func (e *rollbackOrderExecutor) Apply(context.Context, planner.TunPlan) ([]netexecutor.Step, error) {
	return nil, nil
}

func (e *rollbackOrderExecutor) Verify(context.Context, planner.TunPlan) error { return nil }

func (e *rollbackOrderExecutor) Rollback(context.Context, planner.TunPlan) error {
	*e.order = append(*e.order, "executor-rollback")
	return nil
}

func transactionPlanForTest() planner.TunPlan {
	return planner.TunPlan{
		ProfileID: "test-profile",
		Mode:      planner.ModeTun,
		TunDevice: planner.TunDevicePlan{Name: "podlaz0", MTU: 1500, Action: "create"},
		Routes: []planner.TunRoutePlan{{
			Family:      "ipv4",
			Destination: "default",
			Table:       planner.TunRoutingTable,
			Interface:   "podlaz0",
			Action:      "add",
		}},
		PolicyRules: []planner.TunPolicyRulePlan{{
			Family:   "ipv4",
			Priority: planner.TunRulePriority,
			Selector: planner.IPv4DefaultSelector,
			Table:    planner.TunRoutingTable,
			Action:   "add",
		}},
		Steps: []string{"Plan TUN interface podlaz0"},
	}
}

func transactionPlanWithServerBypassForTest() planner.TunPlan {
	plan := transactionPlanForTest()
	plan.Routes = append(plan.Routes, planner.TunRoutePlan{
		Family:      "ipv4",
		Destination: "203.0.113.10/32",
		Table:       planner.MainRoutingTable,
		Gateway:     "192.0.2.1",
		Interface:   "eth0",
		Action:      "add",
	})
	plan.PolicyRules = append(plan.PolicyRules, planner.TunPolicyRulePlan{
		Family:   "ipv4",
		Priority: planner.ServerRulePriority,
		Selector: "to 203.0.113.10/32",
		Table:    planner.MainRoutingTable,
		Action:   "add",
	})
	return plan
}

func fixedClock() func() time.Time {
	current := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	return func() time.Time {
		current = current.Add(time.Millisecond)
		return current
	}
}

func TestTunTransactionPersistsEachAppliedOwnershipStepInsideCompositeApply(t *testing.T) {
	runtimeDir := t.TempDir()
	clock := fixedClock()
	plan := transactionPlanForTest()
	plan.TunDevice.Action = "verify"
	plan.TunAddress = unboundTunAddressPlanForTest()
	plan.TunAddress.LinkIndex = 7
	plan.TunAddress.LinkKind = "tun"
	plan.TunAddress.AppearedAfterCore = true

	result, err := beginTunTransaction(context.Background(), runtimeDir, profile.Profile{ID: "test-profile"}, plan, clock)
	if err != nil {
		t.Fatalf("begin TUN transaction: %v", err)
	}
	executor := &incrementalPersistenceInspectingExecutor{
		t:             t,
		store:         result.Store,
		transactionID: result.TransactionID,
	}

	err = applyVerifyTunTransactionDeferredRollback(context.Background(), result, executor)
	if err == nil || !strings.Contains(err.Error(), "stop after address") {
		t.Fatalf("expected injected post-address failure, got %v", err)
	}
	if !executor.inspected {
		t.Fatal("address ownership was not inspected from durable transaction state during composite apply")
	}
}

func TestTunTransactionRejectsUnownedIncrementalStepBeforePersistence(t *testing.T) {
	runtimeDir := t.TempDir()
	clock := fixedClock()
	plan := transactionPlanForTest()
	result, err := beginTunTransaction(context.Background(), runtimeDir, profile.Profile{ID: "test-profile"}, plan, clock)
	if err != nil {
		t.Fatalf("begin TUN transaction: %v", err)
	}
	executor := &unownedIncrementalStepExecutor{}

	err = applyVerifyTunTransactionDeferredRollback(context.Background(), result, executor)
	if err == nil || !strings.Contains(err.Error(), "unowned applied TUN step") {
		t.Fatalf("expected unowned step rejection, got %v", err)
	}
	tx, _, loadErr := result.Store.Load(result.TransactionID)
	if loadErr != nil {
		t.Fatalf("load transaction: %v", loadErr)
	}
	if len(tx.AppliedSteps) != 0 || tx.Rollback.Available() {
		t.Fatalf("unowned step must not create rollback authority: steps=%#v rollback=%#v", tx.AppliedSteps, tx.Rollback)
	}
}

type incrementalPersistenceInspectingExecutor struct {
	t             *testing.T
	store         txstate.TransactionStore
	transactionID string
	inspected     bool
}

func (e *incrementalPersistenceInspectingExecutor) Apply(context.Context, planner.TunPlan) ([]netexecutor.Step, error) {
	return nil, errors.New("legacy apply path used")
}

func (e *incrementalPersistenceInspectingExecutor) ApplyWithStepSink(_ context.Context, plan planner.TunPlan, sink netexecutor.AppliedStepSink) ([]netexecutor.Step, error) {
	step := netexecutor.Step{
		Kind:   "tun-address",
		Target: tunAddressTarget(plan.TunAddress),
		Owner:  netexecutor.OwnerTunAddress,
	}
	if err := sink(step); err != nil {
		return []netexecutor.Step{step}, err
	}
	tx, _, err := e.store.Load(e.transactionID)
	if err != nil {
		e.t.Fatalf("load transaction inside apply: %v", err)
	}
	if len(tx.AppliedSteps) != 1 || tx.AppliedSteps[0].Kind != "tun-address" {
		e.t.Fatalf("address step must be durable before next mutation: %#v", tx.AppliedSteps)
	}
	if len(tx.Rollback.TUNAddresses) != 1 || tx.Rollback.TUNAddresses[0].CIDR != plan.TunAddress.CIDR {
		e.t.Fatalf("address rollback identity must be durable before next mutation: %#v", tx.Rollback.TUNAddresses)
	}
	e.inspected = true
	return []netexecutor.Step{step}, errors.New("stop after address")
}

func (e *incrementalPersistenceInspectingExecutor) Verify(context.Context, planner.TunPlan) error {
	return nil
}

func (e *incrementalPersistenceInspectingExecutor) Rollback(context.Context, planner.TunPlan) error {
	return nil
}

type unownedIncrementalStepExecutor struct{}

func (unownedIncrementalStepExecutor) Apply(context.Context, planner.TunPlan) ([]netexecutor.Step, error) {
	return nil, errors.New("legacy apply path used")
}

func (unownedIncrementalStepExecutor) ApplyWithStepSink(_ context.Context, plan planner.TunPlan, sink netexecutor.AppliedStepSink) ([]netexecutor.Step, error) {
	step := netexecutor.Step{Kind: "route", Target: routeTarget(plan.Routes[0]), Owner: "foreign:route"}
	return []netexecutor.Step{step}, sink(step)
}

func (unownedIncrementalStepExecutor) Verify(context.Context, planner.TunPlan) error   { return nil }
func (unownedIncrementalStepExecutor) Rollback(context.Context, planner.TunPlan) error { return nil }
