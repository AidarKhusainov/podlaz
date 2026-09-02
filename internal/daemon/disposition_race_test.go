package daemon

import (
	"context"
	"testing"
	"time"
)

func TestAutomaticAdmissionPrecedesLaterExplicitMutation(t *testing.T) {
	lock := newLifecycleOperationLock()
	admission, ok := lock.tryAdmitAutomaticMutation(lock.lifecycleMutationSnapshot().generation)
	if !ok || admission == nil {
		t.Fatal("automatic reconciliation was not admitted")
	}

	finishExplicit, err := lock.beginExternalMutation()
	if err != nil {
		t.Fatalf("declare later explicit mutation: %v", err)
	}
	defer finishExplicit()

	acquired := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		if lock.acquire(ctx) == nil {
			close(acquired)
		}
	}()

	select {
	case <-acquired:
		t.Fatal("later explicit mutation acquired operation token before admitted automatic reconciliation released it")
	case <-time.After(25 * time.Millisecond):
	}

	admission.Release()
	select {
	case <-acquired:
		lock.release()
	case <-time.After(time.Second):
		t.Fatal("later explicit mutation did not acquire token after automatic reconciliation released it")
	}
}

func TestStaleAutomaticGenerationCannotBeAdmitted(t *testing.T) {
	lock := newLifecycleOperationLock()
	staleGeneration := lock.lifecycleMutationSnapshot().generation
	finish, err := lock.beginExternalMutation()
	if err != nil {
		t.Fatalf("declare explicit replacement: %v", err)
	}
	finish()
	if admission, ok := lock.tryAdmitAutomaticMutation(staleGeneration); ok || admission != nil {
		t.Fatal("automatic disposition from the previous lifecycle generation was admitted")
	}
}
