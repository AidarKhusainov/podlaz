package daemon

import (
	"context"
	"errors"
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestRollbackTunHostStateChildAbsentDoesNotMaskIdentityMismatch(t *testing.T) {
	mismatchErr := errors.New("current TUN identity ifindex=8 does not match persisted ifindex=7")
	executor := &childAbsentDecisionExecutor{firstErr: mismatchErr}

	err := rollbackTunHostStateForTransaction(context.Background(), planner.TunPlan{}, executor, childAbsentRollbackTransactionForTest())
	if !errors.Is(err, mismatchErr) {
		t.Fatalf("identity mismatch must remain failure, got %v", err)
	}
	if executor.childAbsentCalls != 0 {
		t.Fatalf("identity mismatch must not enter child-absent retry, calls=%d", executor.childAbsentCalls)
	}
}

func TestRollbackTunHostStateChildAbsentDoesNotMaskMatchedDNSFailure(t *testing.T) {
	dnsErr := errors.New("resolvectl revert podlaz0 failed")
	executor := &childAbsentDecisionExecutor{firstErr: dnsErr}

	err := rollbackTunHostStateForTransaction(context.Background(), planner.TunPlan{}, executor, childAbsentRollbackTransactionForTest())
	if !errors.Is(err, dnsErr) {
		t.Fatalf("DNS rollback failure must remain failure, got %v", err)
	}
	if executor.childAbsentCalls != 0 {
		t.Fatalf("matched-link resource failure must not enter child-absent retry, calls=%d", executor.childAbsentCalls)
	}
}

func TestRollbackTunHostStateChildAbsentConvergesOnlyForTypedMissingLink(t *testing.T) {
	executor := &childAbsentDecisionExecutor{firstErr: fakeTunRollbackLinkAbsentError{err: errors.New("podlaz0 does not exist")}}

	if err := rollbackTunHostStateForTransaction(context.Background(), planner.TunPlan{}, executor, childAbsentRollbackTransactionForTest()); err != nil {
		t.Fatalf("typed missing link with absent child should converge independent cleanup: %v", err)
	}
	if executor.childAbsentCalls != 1 {
		t.Fatalf("typed missing link should enter child-absent retry once, calls=%d", executor.childAbsentCalls)
	}
}

func childAbsentRollbackTransactionForTest() txstate.Transaction {
	return txstate.Transaction{
		Rollback: txstate.RollbackMetadata{
			ChildProcesses: []txstate.ChildProcessRollback{{
				PID:   1 << 30,
				Label: "xray",
				Owner: txstate.TransactionOwner,
			}},
		},
	}
}

type childAbsentDecisionExecutor struct {
	firstErr         error
	childAbsentErr   error
	childAbsentCalls int
}

func (e *childAbsentDecisionExecutor) Apply(context.Context, planner.TunPlan) ([]netexecutor.Step, error) {
	return nil, nil
}

func (e *childAbsentDecisionExecutor) Verify(context.Context, planner.TunPlan) error { return nil }

func (e *childAbsentDecisionExecutor) Rollback(context.Context, planner.TunPlan) error {
	return e.firstErr
}

func (e *childAbsentDecisionExecutor) RollbackResourceScoped(context.Context, planner.TunPlan) error {
	return e.firstErr
}

func (e *childAbsentDecisionExecutor) RollbackResourceScopedChildAbsent(context.Context, planner.TunPlan) error {
	e.childAbsentCalls++
	return e.childAbsentErr
}

type fakeTunRollbackLinkAbsentError struct {
	err error
}

func (e fakeTunRollbackLinkAbsentError) Error() string { return e.err.Error() }
func (e fakeTunRollbackLinkAbsentError) Unwrap() error { return e.err }
func (e fakeTunRollbackLinkAbsentError) IsTunRollbackLinkAbsent() bool { return true }
