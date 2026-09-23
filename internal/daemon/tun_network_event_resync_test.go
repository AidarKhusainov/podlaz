//go:build linux

package daemon

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
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

func TestTunNetworkEdgeBurstSchedulesOneSettledAuthoritativeResync(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	notifications := make(chan tunRevalidationTrigger, 8)
	notify := withTunNetworkSettledResync(ctx, func(trigger tunRevalidationTrigger) {
		notifications <- trigger
	}, 10*time.Millisecond)

	for _, trigger := range []tunRevalidationTrigger{
		tunRevalidationTriggerLink,
		tunRevalidationTriggerAddress,
		tunRevalidationTriggerRoute,
	} {
		notify(trigger)
		select {
		case got := <-notifications:
			if got != trigger {
				t.Fatalf("immediate trigger=%q, want %q", got, trigger)
			}
		case <-time.After(time.Second):
			t.Fatalf("immediate trigger %q was not delivered", trigger)
		}
	}

	select {
	case got := <-notifications:
		if got != tunRevalidationTriggerSourceResync {
			t.Fatalf("settled trigger=%q, want %q", got, tunRevalidationTriggerSourceResync)
		}
	case <-time.After(time.Second):
		t.Fatal("network edge burst did not schedule an authoritative settled resync")
	}

	select {
	case extra := <-notifications:
		t.Fatalf("network edge burst scheduled duplicate settled resync %q", extra)
	case <-time.After(40 * time.Millisecond):
	}
}

func TestTunNetworkSettledResyncIsCancelledWithEventContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	notifications := make(chan tunRevalidationTrigger, 4)
	notify := withTunNetworkSettledResync(ctx, func(trigger tunRevalidationTrigger) {
		notifications <- trigger
	}, 30*time.Millisecond)

	notify(tunRevalidationTriggerRoute)
	select {
	case got := <-notifications:
		if got != tunRevalidationTriggerRoute {
			t.Fatalf("immediate trigger=%q, want route", got)
		}
	case <-time.After(time.Second):
		t.Fatal("immediate route trigger was not delivered")
	}

	cancel()
	select {
	case extra := <-notifications:
		t.Fatalf("cancelled event context delivered stale settled resync %q", extra)
	case <-time.After(80 * time.Millisecond):
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

func TestTunNetworkManagerActiveConnectionStateChangedSchedulesAuthoritativeReproof(t *testing.T) {
	signal := &dbus.Signal{
		Sender: ":1.42",
		Path:   dbus.ObjectPath("/org/freedesktop/NetworkManager/ActiveConnection/7"),
		Name:   networkManagerActiveInterface + "." + networkManagerActiveStateChanged,
		Body:   []any{uint32(2), uint32(1)},
	}
	trigger, ok := tunNetworkManagerActiveSignalTrigger(signal)
	if !ok {
		t.Fatal("NetworkManager active-connection StateChanged was ignored")
	}
	if trigger != tunRevalidationTriggerSourceResync {
		t.Fatalf("trigger=%q, want authoritative source resync", trigger)
	}

	for _, invalid := range []*dbus.Signal{
		nil,
		{Path: signal.Path, Name: signal.Name, Body: []any{uint32(2)}},
		{Path: dbus.ObjectPath("/org/freedesktop/NetworkManager/Devices/7"), Name: signal.Name, Body: signal.Body},
		{Path: signal.Path, Name: "org.freedesktop.NetworkManager.Device.StateChanged", Body: []any{uint32(100), uint32(30), uint32(0)}},
		{Path: signal.Path, Name: signal.Name, Body: []any{"activated", uint32(1)}},
	} {
		if trigger, ok := tunNetworkManagerActiveSignalTrigger(invalid); ok || trigger != "" {
			t.Fatalf("invalid signal %#v produced trigger=%q ok=%v", invalid, trigger, ok)
		}
	}
}
