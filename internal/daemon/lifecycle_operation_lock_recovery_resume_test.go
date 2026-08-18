package daemon

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestLifecycleOperationLockSerializesRecoveryThroughResumeFollowUp(t *testing.T) {
	lock := newLifecycleOperationLock()
	var mu sync.Mutex
	events := []string{}
	appendEvent := func(event string) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	}

	resumeAStarted := make(chan struct{})
	releaseResumeA := make(chan struct{})
	doneA := make(chan struct{})
	go func() {
		defer close(doneA)
		_ = lock.runRecoveryWithFollowUp(
			context.Background(),
			func() api.RecoveryResponse {
				appendEvent("recover-a")
				return api.RecoveryResponse{Mode: "execute"}
			},
			func(response api.RecoveryResponse) api.RecoveryResponse {
				appendEvent("resume-a")
				close(resumeAStarted)
				<-releaseResumeA
				return response
			},
		)
	}()

	select {
	case <-resumeAStarted:
	case <-time.After(time.Second):
		t.Fatal("first recovery did not reach resume follow-up")
	}

	recoverBStarted := make(chan struct{}, 1)
	doneB := make(chan struct{})
	go func() {
		defer close(doneB)
		_ = lock.runRecoveryWithFollowUp(
			context.Background(),
			func() api.RecoveryResponse {
				appendEvent("recover-b")
				recoverBStarted <- struct{}{}
				return api.RecoveryResponse{Mode: "execute"}
			},
			func(response api.RecoveryResponse) api.RecoveryResponse {
				appendEvent("resume-b")
				return response
			},
		)
	}()

	select {
	case <-recoverBStarted:
		t.Fatal("second recovery entered while first resume follow-up still held lifecycle authority")
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseResumeA)
	select {
	case <-doneA:
	case <-time.After(time.Second):
		t.Fatal("first recovery did not finish")
	}
	select {
	case <-doneB:
	case <-time.After(time.Second):
		t.Fatal("second recovery did not finish after first released lifecycle authority")
	}

	mu.Lock()
	got := append([]string(nil), events...)
	mu.Unlock()
	want := []string{"recover-a", "resume-a", "recover-b", "resume-b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("recovery/resume ordering = %#v, want %#v", got, want)
	}
}
