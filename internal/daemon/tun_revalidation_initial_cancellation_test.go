package daemon

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestDisconnectCancelsGenerationOneRevalidationBeforeMutation(t *testing.T) {
	h := newGenerationOneCancellationHarness(t)
	defer h.cleanup()

	h.startConnect()
	h.waitVerificationStarted()

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if _, err := h.lifecycle.Disconnect(ctx); err != nil {
		t.Fatalf("disconnect must preempt generation-one verification: %v", err)
	}
	h.waitVerificationCancelled()
	if !h.base.disconnectCalled() {
		t.Fatal("disconnect did not acquire mutation authority after cancelling generation-one verification")
	}
}

func TestDaemonShutdownCancelsGenerationOneRevalidationBeforeDisconnect(t *testing.T) {
	h := newGenerationOneCancellationHarness(t)
	defer h.cleanup()

	h.startConnect()
	h.waitVerificationStarted()

	// Server.Run first cancels the daemon event/revalidation control context and
	// then performs bounded disconnect. The harness uses the same control path.
	h.cancelControl()
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if _, err := h.lifecycle.Disconnect(ctx); err != nil {
		t.Fatalf("daemon shutdown disconnect must preempt generation-one verification: %v", err)
	}
	h.waitVerificationCancelled()
	if !h.base.disconnectCalled() {
		t.Fatal("daemon shutdown did not proceed to disconnect after cancelling generation-one verification")
	}
}

type generationOneCancellationHarness struct {
	t              *testing.T
	lifecycle      lifecycleService
	base           *generationOneLifecycleFake
	started        chan struct{}
	cancelled      chan struct{}
	release        chan struct{}
	connectDone    chan struct{}
	cancelControl  context.CancelFunc
	cleanupOnce    sync.Once
}

func newGenerationOneCancellationHarness(t *testing.T) *generationOneCancellationHarness {
	t.Helper()
	started := make(chan struct{})
	cancelled := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	var cancelOnce sync.Once
	runtime := newTunRevalidationRuntime(
		func(context.Context) (tunRevalidationObservation, error) {
			return tunRevalidationObservation{fingerprint: tunUplinkFingerprint{
				Interface: "wlan0", InterfaceIndex: 3, Gateway: "192.0.2.1", Addresses: "192.0.2.55/24",
			}}, nil
		},
		func(ctx context.Context, _ tunRevalidationObservation) error {
			startOnce.Do(func() { close(started) })
			select {
			case <-ctx.Done():
				cancelOnce.Do(func() { close(cancelled) })
				return ctx.Err()
			case <-release:
				return nil
			}
		},
	)
	base := &generationOneLifecycleFake{}
	lock := newLifecycleOperationLock()
	controlCtx, cancelControl := context.WithCancel(context.Background())
	coordinator := newTunRevalidationCoordinator(func(ctx context.Context, trigger tunRevalidationTrigger) {
		_ = lock.runRevalidation(ctx, func() {
			runtime.Revalidate(ctx, trigger)
		})
	})
	lock.setRevalidationCancel(coordinator.InterruptForMutation)
	go coordinator.Run(controlCtx)

	health := tunRevalidationLifecycle{lifecycle: base, runtime: runtime}
	return &generationOneCancellationHarness{
		t: t, lifecycle: lock.wrap(health), base: base, started: started, cancelled: cancelled,
		release: release, connectDone: make(chan struct{}), cancelControl: cancelControl,
	}
}

func (h *generationOneCancellationHarness) startConnect() {
	h.t.Helper()
	go func() {
		defer close(h.connectDone)
		_, _ = h.lifecycle.Connect(context.Background(), api.ConnectRequest{Mode: planner.ModeTun})
	}()
}

func (h *generationOneCancellationHarness) waitVerificationStarted() {
	h.t.Helper()
	select {
	case <-h.started:
	case <-time.After(time.Second):
		h.t.Fatal("generation-one verification did not start")
	}
}

func (h *generationOneCancellationHarness) waitVerificationCancelled() {
	h.t.Helper()
	select {
	case <-h.cancelled:
	case <-time.After(time.Second):
		h.t.Fatal("generation-one verification did not observe external cancellation")
	}
}

func (h *generationOneCancellationHarness) cleanup() {
	h.cleanupOnce.Do(func() {
		h.cancelControl()
		close(h.release)
		select {
		case <-h.connectDone:
		case <-time.After(time.Second):
			h.t.Fatal("generation-one connect goroutine did not quiesce")
		}
	})
}

type generationOneLifecycleFake struct {
	mu           sync.Mutex
	disconnected bool
}

func (f *generationOneLifecycleFake) Connect(context.Context, api.ConnectRequest) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{}, nil
}

func (f *generationOneLifecycleFake) Disconnect(context.Context) (api.LifecycleResponse, error) {
	f.mu.Lock()
	f.disconnected = true
	f.mu.Unlock()
	return api.LifecycleResponse{}, nil
}

func (f *generationOneLifecycleFake) disconnectCalled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.disconnected
}
