package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/AidarKhusainov/tunwarden/internal/network/planner"
)

func TestVerifyTunConnectivityChecksRouteAndDial(t *testing.T) {
	originalRouteLookup := lookupTunRouteForProbe
	originalDial := dialTunProbeTarget
	defer func() {
		lookupTunRouteForProbe = originalRouteLookup
		dialTunProbeTarget = originalDial
	}()

	var routeHost string
	var routeDevice string
	var dialHost string
	var dialPort uint16
	lookupTunRouteForProbe = func(_ context.Context, host, tunDevice string) error {
		routeHost = host
		routeDevice = tunDevice
		return nil
	}
	dialTunProbeTarget = func(_ context.Context, host string, port uint16) error {
		dialHost = host
		dialPort = port
		return nil
	}

	err := verifyTunConnectivity(context.Background(), planner.TunPlan{TunDevice: planner.TunDevicePlan{Name: "tunwarden0"}}, tunCoreRuntimePlan{})
	if err != nil {
		t.Fatalf("expected connectivity probe to pass, got %v", err)
	}
	if routeHost != defaultTunProbeHost || routeDevice != "tunwarden0" {
		t.Fatalf("unexpected route lookup target: host=%q device=%q", routeHost, routeDevice)
	}
	if dialHost != defaultTunProbeHost || dialPort != defaultTunProbePort {
		t.Fatalf("unexpected dial target: host=%q port=%d", dialHost, dialPort)
	}
}

func TestVerifyTunConnectivityFailsWhenRouteDoesNotUseTun(t *testing.T) {
	originalRouteLookup := lookupTunRouteForProbe
	originalDial := dialTunProbeTarget
	defer func() {
		lookupTunRouteForProbe = originalRouteLookup
		dialTunProbeTarget = originalDial
	}()

	lookupTunRouteForProbe = func(context.Context, string, string) error {
		return errors.New("route lookup did not use TUN device")
	}
	dialTunProbeTarget = func(context.Context, string, uint16) error {
		t.Fatal("dial must not run when route lookup fails")
		return nil
	}

	err := verifyTunConnectivity(context.Background(), planner.TunPlan{TunDevice: planner.TunDevicePlan{Name: "tunwarden0"}}, tunCoreRuntimePlan{})
	if err == nil {
		t.Fatal("expected connectivity probe to fail")
	}
}

func TestVerifyTunConnectivityFailsWhenDialFails(t *testing.T) {
	originalRouteLookup := lookupTunRouteForProbe
	originalDial := dialTunProbeTarget
	defer func() {
		lookupTunRouteForProbe = originalRouteLookup
		dialTunProbeTarget = originalDial
	}()

	lookupTunRouteForProbe = func(context.Context, string, string) error { return nil }
	dialTunProbeTarget = func(context.Context, string, uint16) error { return errors.New("dial failed") }

	err := verifyTunConnectivity(context.Background(), planner.TunPlan{TunDevice: planner.TunDevicePlan{Name: "tunwarden0"}}, tunCoreRuntimePlan{})
	if err == nil {
		t.Fatal("expected connectivity probe to fail")
	}
}

func TestSelectTunProbeHostAvoidsServerBypassTarget(t *testing.T) {
	plan := planner.TunPlan{ServerBypass: planner.TunRoutePlan{Destination: defaultTunProbeHost + "/32"}}
	if got := selectTunProbeHost(plan); got == defaultTunProbeHost {
		t.Fatalf("expected alternate probe host when default probe is the server bypass target, got %q", got)
	}
}

func TestContainsAdjacentRouteFields(t *testing.T) {
	if !containsAdjacentRouteFields([]string{"1.1.1.1", "dev", "tunwarden0", "src", "10.0.0.2"}, "dev", "tunwarden0") {
		t.Fatal("expected route fields to contain dev tunwarden0")
	}
	if containsAdjacentRouteFields([]string{"1.1.1.1", "dev", "eth0"}, "dev", "tunwarden0") {
		t.Fatal("did not expect route fields to contain dev tunwarden0")
	}
}
