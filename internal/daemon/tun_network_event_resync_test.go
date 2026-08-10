//go:build linux

package daemon

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestTunNetworkEventSourceReconnectSchedulesAuthoritativeResync(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var attempts atomic.Int32
	var resyncs atomic.Int32
	done := make(chan struct{})
	source := func(ctx context.Context, _ tunNetworkEventNotifyFunc, ready tunNetworkEventReadyFunc) error {
		attempt := attempts.Add(1)
		ready()
		if attempt == 1 {
			return errors.New("simulated watcher loss")
		}
		close(done)
		<-ctx.Done()
		return ctx.Err()
	}
	notify := func(trigger tunRevalidationTrigger) {
		if trigger == tunRevalidationTriggerSourceResync {
			resyncs.Add(1)
		}
	}

	go retryTunNetworkEventSourceWithBackoff(ctx, "test", source, notify, time.Millisecond, time.Millisecond)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("event source did not reconnect")
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("event source attempts=%d, want 2", got)
	}
	if got := resyncs.Load(); got != 2 {
		t.Fatalf("authoritative resync notifications=%d, want one after each successful subscription", got)
	}
}

func TestTunRevalidationSourceResyncReprovesSameGeneration(t *testing.T) {
	fingerprint := tunUplinkFingerprint{Interface: "wlan0", InterfaceIndex: 3, Gateway: "192.0.2.1", Addresses: "192.0.2.55/24"}
	verifyCalls := 0
	runtime := newTunRevalidationRuntime(
		func(context.Context) (tunRevalidationObservation, error) {
			return tunRevalidationObservation{fingerprint: fingerprint}, nil
		},
		func(context.Context, tunRevalidationObservation) error {
			verifyCalls++
			return nil
		},
	)
	runtime.Initialize(context.Background())
	if verifyCalls != 1 {
		t.Fatalf("initialize verifier calls=%d, want 1", verifyCalls)
	}
	runtime.Revalidate(context.Background(), tunRevalidationTriggerSourceResync)
	if verifyCalls != 2 {
		t.Fatalf("source resync verifier calls=%d, want 2", verifyCalls)
	}
}
