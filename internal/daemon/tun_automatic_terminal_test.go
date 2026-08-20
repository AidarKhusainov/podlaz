package daemon

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestAutomaticTerminalRejectsStaleNetworkSessionBeforeDiagnostics(t *testing.T) {
	store := seededProtectedNetworkSessionStore(t, networkSessionIntentResume)
	lock := newLifecycleOperationLock()
	before := lock.lifecycleMutationSnapshot()
	admission, ok := lock.tryAdmitAutomaticMutation(before.generation)
	if !ok {
		t.Fatal("automatic terminal admission failed")
	}

	calls := 0
	handler := tunAutomaticTerminalHandler{
		store: store,
		collect: func(context.Context, planner.TunPlan, error) tunFailureDiagnosticSummary {
			calls++
			return tunFailureDiagnosticSummary{}
		},
		teardown: func(context.Context) error {
			calls++
			return nil
		},
	}
	handler.Handle(context.Background(), admission, tunAutomaticDisposition{
		Kind:             tunDecisionTerminal,
		NetworkSessionID: "ffffffffffffffffffffffffffffffff",
		Cause:            errors.New("synthetic terminal evidence"),
	})

	if calls != 0 {
		t.Fatalf("stale session terminal ran %d diagnostic/cleanup calls", calls)
	}
	state, exists, err := store.Load()
	if err != nil || !exists || state.Intent != networkSessionIntentResume {
		t.Fatalf("stale terminal changed durable session: exists=%v state=%#v err=%v", exists, state, err)
	}
	if got := len(lock.token); got != 1 {
		t.Fatalf("stale terminal did not release operation token: len=%d", got)
	}
}

func TestAutomaticTerminalRejectsStaleTransactionBeforeDiagnostics(t *testing.T) {
	store := seededProtectedNetworkSessionStore(t, networkSessionIntentResume)
	state, _, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	lock := newLifecycleOperationLock()
	admission, ok := lock.tryAdmitAutomaticMutation(lock.lifecycleMutationSnapshot().generation)
	if !ok {
		t.Fatal("automatic terminal admission failed")
	}

	calls := 0
	handler := tunAutomaticTerminalHandler{
		store:                store,
		currentTransactionID: func() string { return "tx-current" },
		collect: func(context.Context, planner.TunPlan, error) tunFailureDiagnosticSummary {
			calls++
			return tunFailureDiagnosticSummary{}
		},
		teardown: func(context.Context) error { calls++; return nil },
	}
	handler.Handle(context.Background(), admission, tunAutomaticDisposition{
		Kind:             tunDecisionTerminal,
		NetworkSessionID: state.SessionID,
		TransactionID:    "tx-stale",
		Cause:            errors.New("synthetic terminal evidence"),
	})

	if calls != 0 {
		t.Fatalf("stale transaction terminal ran %d diagnostic/cleanup calls", calls)
	}
	if got := len(lock.token); got != 1 {
		t.Fatalf("stale transaction terminal did not release operation token: len=%d", got)
	}
}

func TestAutomaticTerminalRunsDiagnosticsThenUnwrappedTeardownUnderOneAdmission(t *testing.T) {
	store := seededProtectedNetworkSessionStore(t, networkSessionIntentResume)
	state, _, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	lock := newLifecycleOperationLock()
	before := lock.lifecycleMutationSnapshot()
	admission, ok := lock.tryAdmitAutomaticMutation(before.generation)
	if !ok {
		t.Fatal("automatic terminal admission failed")
	}
	admittedGeneration := lock.lifecycleMutationSnapshot().generation

	var events []string
	handler := tunAutomaticTerminalHandler{
		store:                store,
		currentTransactionID: func() string { return "tx-a" },
		collect: func(ctx context.Context, plan planner.TunPlan, cause error) tunFailureDiagnosticSummary {
			events = append(events, "diagnostics")
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("automatic terminal diagnostics are not bounded")
			}
			if plan.Mode != planner.ModeTun || cause == nil {
				t.Fatalf("terminal diagnostics lost evidence: plan=%#v cause=%v", plan, cause)
			}
			return tunFailureDiagnosticSummary{Persisted: true}
		},
		teardown: func(ctx context.Context) error {
			events = append(events, "teardown")
			if !isTerminalNetworkSessionTeardown(ctx) {
				t.Fatal("automatic terminal teardown did not carry terminal context")
			}
			if got := len(lock.token); got != 0 {
				t.Fatalf("operation token became available during admitted teardown: len=%d", got)
			}
			return nil
		},
		finalize: func(_ context.Context, _ tunFailureDiagnosticSummary, status string) {
			events = append(events, "finalize:"+status)
		},
	}
	handler.Handle(context.Background(), admission, tunAutomaticDisposition{
		Kind:             tunDecisionTerminal,
		NetworkSessionID: state.SessionID,
		TransactionID:    "tx-a",
		Plan:             planner.TunPlan{Mode: planner.ModeTun},
		Cause:            errors.New("synthetic terminal evidence"),
	})

	want := []string{"diagnostics", "teardown", "finalize:completed"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("automatic terminal ordering=%v, want %v", events, want)
	}
	if got := lock.lifecycleMutationSnapshot().generation; got != admittedGeneration {
		t.Fatalf("automatic terminal re-registered lifecycle mutation: generation=%d, want %d", got, admittedGeneration)
	}
	if got := len(lock.token); got != 1 {
		t.Fatalf("automatic terminal did not release operation token: len=%d", got)
	}
}

func TestAutomaticTerminalCleanupFailureMarksCleanupRequiredAndKeepsAdmissionBounded(t *testing.T) {
	store := seededProtectedNetworkSessionStore(t, networkSessionIntentResume)
	state, _, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	lock := newLifecycleOperationLock()
	admission, ok := lock.tryAdmitAutomaticMutation(lock.lifecycleMutationSnapshot().generation)
	if !ok {
		t.Fatal("automatic terminal admission failed")
	}

	marked := false
	finalized := ""
	handler := tunAutomaticTerminalHandler{
		store: store,
		collect: func(context.Context, planner.TunPlan, error) tunFailureDiagnosticSummary {
			return tunFailureDiagnosticSummary{Persisted: true}
		},
		teardown: func(ctx context.Context) error {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > tunRollbackCleanupTimeout {
				t.Fatalf("terminal cleanup deadline is not bounded: %v", deadline)
			}
			return errors.New("synthetic exact teardown failure")
		},
		markCleanupRequired: func(disposition tunAutomaticDisposition) {
			marked = disposition.NetworkSessionID == state.SessionID
		},
		finalize: func(_ context.Context, _ tunFailureDiagnosticSummary, status string) {
			finalized = status
		},
	}
	handler.Handle(context.Background(), admission, tunAutomaticDisposition{
		Kind:             tunDecisionTerminal,
		NetworkSessionID: state.SessionID,
		Cause:            errors.New("synthetic terminal evidence"),
	})

	if !marked || finalized != "failed" {
		t.Fatalf("cleanup failure publication marked=%v finalized=%q", marked, finalized)
	}
	if got := len(lock.token); got != 1 {
		t.Fatalf("cleanup failure leaked automatic operation token: len=%d", got)
	}
}
