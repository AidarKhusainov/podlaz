package daemon

import (
	"context"
	"testing"
	"time"
)

func TestIssue262ExplicitMutationBetweenRoundAndAdmissionRequeuesReconciliation(t *testing.T) {
	lock := newLifecycleOperationLock()
	expectedGeneration := lock.lifecycleMutationSnapshot().generation
	admitEntered := make(chan struct{})
	releaseAdmit := make(chan struct{})
	secondRun := make(chan tunRevalidationTrigger, 1)
	runs := 0

	coordinator := newTunAutomaticDispositionCoordinator(
		func(context.Context, tunRevalidationTrigger) tunAutomaticDisposition {
			runs++
			if runs == 1 {
				return tunAutomaticDisposition{
					Kind:                       tunDecisionReconcile,
					ExpectedMutationGeneration: expectedGeneration,
					NetworkSessionID:           "session-a",
				}
			}
			secondRun <- tunRevalidationTriggerSourceResync
			return tunAutomaticDisposition{}
		},
		func(expected uint64) (*lifecycleAutomaticAdmission, bool) {
			close(admitEntered)
			<-releaseAdmit
			return lock.tryAdmitAutomaticMutation(expected)
		},
		func(_ context.Context, admission *lifecycleAutomaticAdmission, _ tunAutomaticDisposition) {
			admission.Release()
		},
	)
	lock.setRevalidationCancel(coordinator.InterruptForMutation)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go coordinator.Run(ctx)
	coordinator.Notify(tunRevalidationTriggerSourceResync)

	select {
	case <-admitEntered:
	case <-time.After(time.Second):
		t.Fatal("automatic admission was not reached")
	}

	mutationDone := make(chan error, 1)
	go func() {
		finish, err := lock.beginExternalMutation()
		if err != nil {
			mutationDone <- err
			return
		}
		finish()
		mutationDone <- nil
	}()

	deadline := time.Now().Add(time.Second)
	for lock.lifecycleMutationSnapshot().generation == expectedGeneration {
		if time.Now().After(deadline) {
			t.Fatal("explicit mutation did not supersede automatic generation")
		}
		time.Sleep(time.Millisecond)
	}
	close(releaseAdmit)

	select {
	case err := <-mutationDone:
		if err != nil {
			t.Fatalf("explicit mutation failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("explicit mutation remained blocked after stale admission was rejected")
	}

	select {
	case <-secondRun:
	case <-time.After(time.Second):
		t.Fatal("superseded automatic disposition lost its reconciliation trigger")
	}
}
