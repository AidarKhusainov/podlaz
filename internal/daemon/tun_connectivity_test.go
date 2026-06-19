package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestVerifyTunConnectivityChecksRouteDialAndDNS(t *testing.T) {
	originalRouteLookup := lookupTunRouteForProbe
	originalDial := dialTunProbeTarget
	originalResolve := resolveTunDNSName
	defer func() {
		lookupTunRouteForProbe = originalRouteLookup
		dialTunProbeTarget = originalDial
		resolveTunDNSName = originalResolve
	}()

	var routeLookups []struct {
		host   string
		device string
	}
	var dialHost string
	var dialPort uint16
	var resolvedName string
	lookupTunRouteForProbe = func(_ context.Context, host, tunDevice string) error {
		routeLookups = append(routeLookups, struct {
			host   string
			device string
		}{host: host, device: tunDevice})
		return nil
	}
	dialTunProbeTarget = func(_ context.Context, host string, port uint16) error {
		dialHost = host
		dialPort = port
		return nil
	}
	resolveTunDNSName = func(_ context.Context, name string) (string, error) {
		resolvedName = name
		return "93.184.216.34", nil
	}

	err := verifyTunConnectivity(context.Background(), planner.TunPlan{TunDevice: planner.TunDevicePlan{Name: "podlaz0"}}, tunCoreRuntimePlan{})
	if err != nil {
		t.Fatalf("expected connectivity probe to pass, got %v", err)
	}
	if len(routeLookups) != 2 {
		t.Fatalf("expected route lookup for probe IP and DNS result, got %#v", routeLookups)
	}
	if routeLookups[0].host != defaultTunProbeHost || routeLookups[0].device != "podlaz0" {
		t.Fatalf("unexpected route lookup target: %#v", routeLookups[0])
	}
	if routeLookups[1].host != "93.184.216.34" || routeLookups[1].device != "podlaz0" {
		t.Fatalf("unexpected DNS-result route lookup target: %#v", routeLookups[1])
	}
	if dialHost != defaultTunProbeHost || dialPort != defaultTunProbePort {
		t.Fatalf("unexpected dial target: host=%q port=%d", dialHost, dialPort)
	}
	if resolvedName != defaultTunDNSProbeName {
		t.Fatalf("unexpected DNS probe name: %q", resolvedName)
	}
}

func TestVerifyTunConnectivityFailsWhenRouteDoesNotUseTun(t *testing.T) {
	originalRouteLookup := lookupTunRouteForProbe
	originalDial := dialTunProbeTarget
	originalResolve := resolveTunDNSName
	defer func() {
		lookupTunRouteForProbe = originalRouteLookup
		dialTunProbeTarget = originalDial
		resolveTunDNSName = originalResolve
	}()

	lookupTunRouteForProbe = func(context.Context, string, string) error {
		return errors.New("route lookup did not use TUN device")
	}
	dialTunProbeTarget = func(context.Context, string, uint16) error {
		t.Fatal("dial must not run when route lookup fails")
		return nil
	}
	resolveTunDNSName = func(context.Context, string) (string, error) {
		t.Fatal("DNS probe must not run when route lookup fails")
		return "", nil
	}

	err := verifyTunConnectivity(context.Background(), planner.TunPlan{TunDevice: planner.TunDevicePlan{Name: "podlaz0"}}, tunCoreRuntimePlan{})
	if err == nil {
		t.Fatal("expected connectivity probe to fail")
	}
}

func TestVerifyTunConnectivityFailsWhenDialFails(t *testing.T) {
	originalRouteLookup := lookupTunRouteForProbe
	originalDial := dialTunProbeTarget
	originalResolve := resolveTunDNSName
	defer func() {
		lookupTunRouteForProbe = originalRouteLookup
		dialTunProbeTarget = originalDial
		resolveTunDNSName = originalResolve
	}()

	lookupTunRouteForProbe = func(context.Context, string, string) error { return nil }
	dialTunProbeTarget = func(context.Context, string, uint16) error { return errors.New("dial failed") }
	resolveTunDNSName = func(context.Context, string) (string, error) {
		t.Fatal("DNS probe must not run when TCP probe fails")
		return "", nil
	}

	err := verifyTunConnectivity(context.Background(), planner.TunPlan{TunDevice: planner.TunDevicePlan{Name: "podlaz0"}}, tunCoreRuntimePlan{})
	if err == nil {
		t.Fatal("expected connectivity probe to fail")
	}
}

func TestVerifyTunConnectivityFailsWhenDNSFails(t *testing.T) {
	originalRouteLookup := lookupTunRouteForProbe
	originalDial := dialTunProbeTarget
	originalResolve := resolveTunDNSName
	defer func() {
		lookupTunRouteForProbe = originalRouteLookup
		dialTunProbeTarget = originalDial
		resolveTunDNSName = originalResolve
	}()

	lookupTunRouteForProbe = func(context.Context, string, string) error { return nil }
	dialTunProbeTarget = func(context.Context, string, uint16) error { return nil }
	resolveTunDNSName = func(context.Context, string) (string, error) { return "", errors.New("dns timeout") }

	err := verifyTunConnectivity(context.Background(), planner.TunPlan{TunDevice: planner.TunDevicePlan{Name: "podlaz0"}}, tunCoreRuntimePlan{})
	if err == nil {
		t.Fatal("expected connectivity probe to fail")
	}
}

func TestVerifyTunConnectivityFailsWhenDNSResultDoesNotRouteThroughTun(t *testing.T) {
	originalRouteLookup := lookupTunRouteForProbe
	originalDial := dialTunProbeTarget
	originalResolve := resolveTunDNSName
	defer func() {
		lookupTunRouteForProbe = originalRouteLookup
		dialTunProbeTarget = originalDial
		resolveTunDNSName = originalResolve
	}()

	lookupCalls := 0
	lookupTunRouteForProbe = func(_ context.Context, host, _ string) error {
		lookupCalls++
		if host == "93.184.216.34" {
			return errors.New("route lookup did not use TUN device")
		}
		return nil
	}
	dialTunProbeTarget = func(context.Context, string, uint16) error { return nil }
	resolveTunDNSName = func(context.Context, string) (string, error) { return "93.184.216.34", nil }

	err := verifyTunConnectivity(context.Background(), planner.TunPlan{TunDevice: planner.TunDevicePlan{Name: "podlaz0"}}, tunCoreRuntimePlan{})
	if err == nil {
		t.Fatal("expected connectivity probe to fail")
	}
	if lookupCalls != 2 {
		t.Fatalf("expected two route lookups, got %d", lookupCalls)
	}
}

func TestSelectTunProbeHostAvoidsServerBypassTarget(t *testing.T) {
	plan := planner.TunPlan{ServerBypass: planner.TunRoutePlan{Destination: defaultTunProbeHost + "/32"}}
	if got := selectTunProbeHost(plan); got == defaultTunProbeHost {
		t.Fatalf("expected alternate probe host when default probe is the server bypass target, got %q", got)
	}
}

func TestContainsAdjacentRouteFields(t *testing.T) {
	if !containsAdjacentRouteFields([]string{"1.1.1.1", "dev", "podlaz0", "src", "10.0.0.2"}, "dev", "podlaz0") {
		t.Fatal("expected route fields to contain dev podlaz0")
	}
	if containsAdjacentRouteFields([]string{"1.1.1.1", "dev", "eth0"}, "dev", "podlaz0") {
		t.Fatal("did not expect route fields to contain dev podlaz0")
	}
}
