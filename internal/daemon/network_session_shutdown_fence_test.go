package daemon

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

type shutdownFenceRecordingLifecycle struct {
	mu                sync.Mutex
	events            []string
	connectStarted    chan struct{}
	releaseConnect    chan struct{}
	disconnectStarted chan struct{}
}

func (l *shutdownFenceRecordingLifecycle) append(event string) {
	l.mu.Lock()
	l.events = append(l.events, event)
	l.mu.Unlock()
}

func (l *shutdownFenceRecordingLifecycle) Connect(ctx context.Context, _ api.ConnectRequest) (api.LifecycleResponse, error) {
	l.append("connect-start")
	select {
	case <-l.connectStarted:
	default:
		close(l.connectStarted)
	}
	select {
	case <-l.releaseConnect:
		l.append("connect-finish")
		return api.LifecycleResponse{Connection: "active", Proxy: "active", TUN: "active"}, nil
	case <-ctx.Done():
		return api.LifecycleResponse{}, ctx.Err()
	}
}

func (l *shutdownFenceRecordingLifecycle) Disconnect(context.Context) (api.LifecycleResponse, error) {
	l.append("disconnect")
	select {
	case <-l.disconnectStarted:
	default:
		close(l.disconnectStarted)
	}
	return api.LifecycleResponse{Connection: "inactive", Proxy: "inactive", TUN: "disabled"}, nil
}

func (l *shutdownFenceRecordingLifecycle) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

func TestShutdownFencesNewMutationsDrainsAcceptedConnectThenTeardownRunsLast(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	inner := &shutdownFenceRecordingLifecycle{
		connectStarted:    make(chan struct{}),
		releaseConnect:    make(chan struct{}),
		disconnectStarted: make(chan struct{}),
	}
	session := newNetworkSessionLifecycle(inner, store)
	lock := newLifecycleOperationLock()
	locked := lock.wrap(session)

	connectDone := make(chan error, 1)
	go func() {
		_, err := locked.Connect(context.Background(), testContinuationRequest())
		connectDone <- err
	}()
	select {
	case <-inner.connectStarted:
	case <-time.After(time.Second):
		t.Fatal("accepted connect did not enter lifecycle mutation")
	}

	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- shutdownDaemonServer(
			context.Background(),
			&http.Server{},
			make(chan error),
			0,
			func() {},
			lock,
			session,
			ShutdownStop,
			nil,
		)
	}()

	deadline := time.Now().Add(time.Second)
	for !lock.mutationsFenced() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !lock.mutationsFenced() {
		t.Fatal("shutdown did not establish lifecycle mutation fence")
	}

	secondDone := make(chan error, 1)
	go func() {
		_, err := locked.Connect(context.Background(), testContinuationRequest())
		secondDone <- err
	}()
	select {
	case err := <-secondDone:
		if !errors.Is(err, errLifecycleShuttingDown) {
			t.Fatalf("new mutation after shutdown fence error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("new mutation after shutdown fence blocked instead of failing closed")
	}

	select {
	case <-inner.disconnectStarted:
		t.Fatal("final teardown started before already-accepted connect drained")
	default:
	}

	close(inner.releaseConnect)
	select {
	case err := <-connectDone:
		if err != nil {
			t.Fatalf("accepted connect failed while draining: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("accepted connect did not drain")
	}
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("shutdown failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish after accepted mutation drained")
	}

	want := []string{"connect-start", "connect-finish", "disconnect"}
	if got := inner.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("shutdown lifecycle ordering = %#v, want %#v", got, want)
	}
	if _, exists, err := store.LoadCurrent(); err != nil || exists {
		t.Fatalf("final explicit-stop teardown must leave continuation disarmed, exists=%v err=%v", exists, err)
	}
}
