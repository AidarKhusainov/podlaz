package daemon

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestBootAutostartReadinessWaitsWithinSameLogicalAttempt(t *testing.T) {
	observations := 0
	waiter := bootAutostartReadinessWaiter{
		probe: func(context.Context) (bool, error) {
			observations++
			return observations >= 3, nil
		},
		timeout:  time.Second,
		interval: time.Millisecond,
	}

	if err := waiter.Wait(context.Background()); err != nil {
		t.Fatalf("wait for readiness: %v", err)
	}
	if observations != 3 {
		t.Fatalf("readiness observations = %d, want 3", observations)
	}
}

func TestBootAutostartReadinessTimeoutConsumesAttemptWithoutConnect(t *testing.T) {
	manifestStore, attemptStore, continuation := bootAutostartStores(t, testBootConfigured, testBootAttempt)
	if _, err := manifestStore.Enable(testBootAutostartConfig()); err != nil {
		t.Fatal(err)
	}
	lifecycle := &bootAutostartRecordingLifecycle{}

	result, err := runBootAutostartStartupWithOptions(
		context.Background(), manifestStore, attemptStore, continuation, lifecycle,
		func(context.Context) (bool, error) { return false, nil },
		bootAutostartStartupOptions{
			terminalConverge: successfulBootTerminalConvergence(t),
			waitForNetwork: func(context.Context) error {
				return errBootAutostartNetworkNotReady
			},
		},
	)
	if !errors.Is(err, errBootAutostartNetworkNotReady) {
		t.Fatalf("readiness timeout error = %v", err)
	}
	if result != bootAutostartStartupTerminal {
		t.Fatalf("result = %q, want terminal", result)
	}
	if len(lifecycle.requests) != 0 {
		t.Fatalf("readiness timeout called Connect %d time(s)", len(lifecycle.requests))
	}
	attempt, exists, loadErr := attemptStore.LoadCurrent()
	if loadErr != nil || !exists {
		t.Fatalf("load attempt: exists=%v err=%v", exists, loadErr)
	}
	if attempt.State != bootAutostartAttemptTerminal || attempt.TerminalReason != bootAutostartTerminalNetworkNotReady {
		t.Fatalf("attempt after readiness timeout = %+v", attempt)
	}
	if _, exists, loadErr := continuation.stateStore().Load(); loadErr != nil || exists {
		t.Fatalf("readiness timeout left Network Session authority: exists=%v err=%v", exists, loadErr)
	}
}

func TestBootAutostartReadinessCancellationKeepsAttemptInProgressWithoutConnect(t *testing.T) {
	manifestStore, attemptStore, continuation := bootAutostartStores(t, testBootConfigured, testBootAttempt)
	if _, err := manifestStore.Enable(testBootAutostartConfig()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	lifecycle := &bootAutostartRecordingLifecycle{}

	result, err := runBootAutostartStartupWithOptions(
		ctx, manifestStore, attemptStore, continuation, lifecycle,
		func(context.Context) (bool, error) { return false, nil },
		bootAutostartStartupOptions{
			waitForNetwork: func(context.Context) error {
				cancel()
				return context.Canceled
			},
		},
	)
	if !errors.Is(err, context.Canceled) || result != bootAutostartStartupContinued {
		t.Fatalf("canceled readiness result=%q err=%v", result, err)
	}
	if len(lifecycle.requests) != 0 {
		t.Fatalf("canceled readiness called Connect %d time(s)", len(lifecycle.requests))
	}
	attempt, exists, loadErr := attemptStore.LoadCurrent()
	if loadErr != nil || !exists || attempt.State != bootAutostartAttemptInProgress {
		t.Fatalf("attempt after canceled readiness = %+v exists=%v err=%v", attempt, exists, loadErr)
	}
	if _, exists, loadErr := continuation.stateStore().Load(); loadErr != nil || exists {
		t.Fatalf("canceled pre-connect readiness created continuation: exists=%v err=%v", exists, loadErr)
	}
}

func TestBootNetworkProbeRequiresDefaultRouteAndGlobalIPv4(t *testing.T) {
	probe := bootNetworkReadinessProbe{
		readRoutes: func() ([]byte, error) {
			return []byte("Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
				"wlan0\t00000000\t01020304\t0003\t0\t0\t600\t00000000\t0\t0\t0\n"), nil
		},
		interfaceAddrs: func(name string) ([]net.Addr, error) {
			if name != "wlan0" {
				t.Fatalf("interface = %q, want wlan0", name)
			}
			return []net.Addr{&net.IPNet{IP: net.ParseIP("192.0.2.10"), Mask: net.CIDRMask(24, 32)}}, nil
		},
	}
	ready, err := probe.Ready(context.Background())
	if err != nil || !ready {
		t.Fatalf("ready=%v err=%v, want usable default route", ready, err)
	}
}

func TestBootNetworkProbeRejectsRouteBeforeAddressAssignment(t *testing.T) {
	probe := bootNetworkReadinessProbe{
		readRoutes: func() ([]byte, error) {
			return []byte("Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
				"wlan0\t00000000\t01020304\t0003\t0\t0\t600\t00000000\t0\t0\t0\n"), nil
		},
		interfaceAddrs: func(string) ([]net.Addr, error) { return nil, nil },
	}
	ready, err := probe.Ready(context.Background())
	if err != nil {
		t.Fatalf("probe error: %v", err)
	}
	if ready {
		t.Fatal("default route without assigned IPv4 must not be boot-ready")
	}
}
