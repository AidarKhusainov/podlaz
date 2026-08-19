package daemon

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

type blockingDisconnectLifecycle struct {
	events  *[]string
	started chan struct{}
	release chan struct{}
	err     error
}

func (l blockingDisconnectLifecycle) Connect(context.Context, api.ConnectRequest) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{}, nil
}

func (l blockingDisconnectLifecycle) Disconnect(ctx context.Context) (api.LifecycleResponse, error) {
	*l.events = append(*l.events, "disconnect-start")
	close(l.started)
	select {
	case <-l.release:
		*l.events = append(*l.events, "disconnect-finish")
		return api.LifecycleResponse{Connection: "inactive"}, l.err
	case <-ctx.Done():
		return api.LifecycleResponse{}, ctx.Err()
	}
}

func TestShutdownWaitsForRestartTeardownAndPreservesContinuation(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if err := store.Save(testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	events := []string{}
	inner := blockingDisconnectLifecycle{
		events:  &events,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	session := newNetworkSessionLifecycle(inner, store)
	lock := newLifecycleOperationLock()
	done := make(chan error, 1)
	go func() {
		done <- shutdownDaemonServer(
			context.Background(),
			&http.Server{},
			make(chan error),
			0,
			func() {},
			lock,
			session,
			ShutdownRestart,
			nil,
		)
	}()

	select {
	case <-inner.started:
	case <-time.After(time.Second):
		t.Fatal("restart teardown did not start")
	}
	select {
	case err := <-done:
		t.Fatalf("shutdown returned before teardown converged: %v", err)
	default:
	}
	close(inner.release)
	if err := <-done; err != nil {
		t.Fatalf("restart shutdown: %v", err)
	}
	if _, ok, err := store.LoadCurrent(); err != nil || !ok {
		t.Fatalf("restart shutdown must retain continuation, ok=%v err=%v", ok, err)
	}
	want := []string{"disconnect-start", "disconnect-finish"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("restart teardown ordering = %#v, want %#v", events, want)
	}
}

func TestExplicitStopDisarmsBeforeTeardownAndPropagatesFailure(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if err := store.Save(testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	events := []string{}
	store.afterRemove = func() { events = append(events, "continuation-removed") }
	inner := networkSessionRecordingLifecycle{events: &events, disconnectErr: errors.New("exact rollback failed")}
	session := newNetworkSessionLifecycle(inner, store)

	err := shutdownDaemonServer(
		context.Background(),
		&http.Server{},
		make(chan error),
		0,
		func() {},
		newLifecycleOperationLock(),
		session,
		ShutdownStop,
		nil,
	)
	if err == nil || !errors.Is(err, inner.disconnectErr) {
		t.Fatalf("shutdown must propagate rollback failure, got %v", err)
	}
	want := []string{"continuation-removed", "disconnect"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("explicit stop ordering = %#v, want %#v", events, want)
	}
	if _, ok, loadErr := store.LoadCurrent(); loadErr != nil || ok {
		t.Fatalf("explicit stop must remain disarmed, ok=%v err=%v", ok, loadErr)
	}
}
