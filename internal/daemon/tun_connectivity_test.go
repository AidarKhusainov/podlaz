package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestVerifyTunConnectivityChecksRouteDialAndDNS(t *testing.T) {
	stubScopedDNSForConnectivityTest(t)
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
	resolveTunDNSName = func(_ context.Context, name string) ([]string, error) {
		resolvedName = name
		return []string{"93.184.216.34"}, nil
	}

	err := verifyTunConnectivity(context.Background(), addressedTunPlanForConnectivityTest(), tunCoreRuntimePlan{})
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

func TestVerifyTunConnectivityUsesConfiguredProbeTargets(t *testing.T) {
	stubScopedDNSForConnectivityTest(t)
	originalRouteLookup := lookupTunRouteForProbe
	originalDial := dialTunProbeTarget
	originalResolve := resolveTunDNSName
	defer func() {
		lookupTunRouteForProbe = originalRouteLookup
		dialTunProbeTarget = originalDial
		resolveTunDNSName = originalResolve
	}()

	var routeHosts []string
	var dialHost string
	var dialPort uint16
	var resolvedName string
	lookupTunRouteForProbe = func(_ context.Context, host, _ string) error {
		routeHosts = append(routeHosts, host)
		return nil
	}
	dialTunProbeTarget = func(_ context.Context, host string, port uint16) error {
		dialHost = host
		dialPort = port
		return nil
	}
	resolveTunDNSName = func(_ context.Context, name string) ([]string, error) {
		resolvedName = name
		return []string{"198.51.100.30"}, nil
	}

	core := tunCoreRuntimePlan{ConnectivityProbe: tunConnectivityProbeConfig{
		RouteHost:    "198.51.100.20",
		TCPPort:      443,
		DNSName:      "probe.example.com",
		RouteTimeout: time.Second,
		TCPTimeout:   time.Second,
		DNSTimeout:   time.Second,
	}}
	if err := verifyTunConnectivity(context.Background(), addressedTunPlanForConnectivityTest(), core); err != nil {
		t.Fatalf("expected configured connectivity probe to pass, got %v", err)
	}
	if len(routeHosts) != 2 || routeHosts[0] != "198.51.100.20" || routeHosts[1] != "198.51.100.30" {
		t.Fatalf("unexpected route hosts: %#v", routeHosts)
	}
	if dialHost != "198.51.100.20" || dialPort != 443 {
		t.Fatalf("unexpected dial target: host=%q port=%d", dialHost, dialPort)
	}
	if resolvedName != "probe.example.com" {
		t.Fatalf("unexpected DNS probe name: %q", resolvedName)
	}
}

func TestVerifyTunConnectivityFailsWhenRouteDoesNotUseTun(t *testing.T) {
	stubScopedDNSForConnectivityTest(t)
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
	resolveTunDNSName = func(context.Context, string) ([]string, error) {
		t.Fatal("DNS probe must not run when route lookup fails")
		return nil, nil
	}

	err := verifyTunConnectivity(context.Background(), addressedTunPlanForConnectivityTest(), tunCoreRuntimePlan{})
	if err == nil {
		t.Fatal("expected connectivity probe to fail")
	}
}

func TestVerifyTunConnectivityFailsWhenDialFails(t *testing.T) {
	stubScopedDNSForConnectivityTest(t)
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
	resolveTunDNSName = func(context.Context, string) ([]string, error) {
		t.Fatal("DNS probe must not run when TCP probe fails")
		return nil, nil
	}

	err := verifyTunConnectivity(context.Background(), addressedTunPlanForConnectivityTest(), tunCoreRuntimePlan{})
	if err == nil {
		t.Fatal("expected connectivity probe to fail")
	}
}

func TestVerifyTunConnectivityFailsWhenDNSFails(t *testing.T) {
	stubScopedDNSForConnectivityTest(t)
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
	resolveTunDNSName = func(context.Context, string) ([]string, error) { return nil, errors.New("dns timeout") }

	err := verifyTunConnectivity(context.Background(), addressedTunPlanForConnectivityTest(), tunCoreRuntimePlan{})
	if err == nil {
		t.Fatal("expected connectivity probe to fail")
	}
}

func TestVerifyTunConnectivityFailsWhenDNSResultDoesNotRouteThroughTun(t *testing.T) {
	stubScopedDNSForConnectivityTest(t)
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
	resolveTunDNSName = func(context.Context, string) ([]string, error) { return []string{"93.184.216.34"}, nil }

	err := verifyTunConnectivity(context.Background(), addressedTunPlanForConnectivityTest(), tunCoreRuntimePlan{})
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

func TestSelectTunProbeHostUsesConfiguredAlternateTarget(t *testing.T) {
	probe := tunConnectivityProbeConfig{RouteHost: "198.51.100.20", AlternateHost: "198.51.100.21"}
	plan := planner.TunPlan{ServerBypass: planner.TunRoutePlan{Destination: "198.51.100.20/32"}}
	if got := selectTunProbeHostWithConfig(plan, probe); got != "198.51.100.21" {
		t.Fatalf("expected configured alternate probe host, got %q", got)
	}
}

func TestSanitizeConnectivityDiagnosticRedactsPrivateNetworkDetails(t *testing.T) {
	input := "default via 10.0.0.1 dev wlp3s0 src 192.168.1.20 search corp.internal query api.private.example.net vpn.example.test example.com"
	got := sanitizeConnectivityDiagnostic(input)

	for _, forbidden := range []string{"10.0.0.1", "192.168.1.20", "corp.internal", "api.private.example.net"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("sanitized diagnostics leaked %q: %q", forbidden, got)
		}
	}
	for _, want := range []string{"<private-ipv4>", "<domain>", "vpn.example.test", "example.com"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected sanitized diagnostics to contain %q, got %q", want, got)
		}
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

func stubScopedDNSForConnectivityTest(t *testing.T) {
	t.Helper()
	original := resolveTunDNSNameScoped
	resolveTunDNSNameScoped = func(context.Context, planner.TunAddressPlan, string) ([]string, error) {
		return []string{"198.51.100.30"}, nil
	}
	t.Cleanup(func() { resolveTunDNSNameScoped = original })
}

func TestVerifyTunConnectivityRejectsMissingTunAddressBeforeDNS(t *testing.T) {
	originalRouteLookup := lookupTunRouteForProbe
	originalDial := dialTunProbeTarget
	originalScoped := resolveTunDNSNameScoped
	originalResolve := resolveTunDNSName
	defer func() {
		lookupTunRouteForProbe = originalRouteLookup
		dialTunProbeTarget = originalDial
		resolveTunDNSNameScoped = originalScoped
		resolveTunDNSName = originalResolve
	}()

	lookupTunRouteForProbe = func(context.Context, string, string) error { return nil }
	dialTunProbeTarget = func(context.Context, string, uint16) error { return nil }
	resolveTunDNSNameScoped = func(context.Context, planner.TunAddressPlan, string) ([]string, error) {
		t.Fatal("scoped DNS must not run when the planned address identity is absent")
		return nil, nil
	}
	resolveTunDNSName = func(context.Context, string) ([]string, error) {
		t.Fatal("system resolver must not run when the planned address identity is absent")
		return nil, nil
	}

	err := verifyTunConnectivity(context.Background(), planner.TunPlan{TunDevice: planner.TunDevicePlan{Name: "podlaz0"}}, tunCoreRuntimePlan{})
	if !errors.Is(err, netexecutor.ErrResolvedLinkNotReady) {
		t.Fatalf("expected resolved-link readiness failure, got %v", err)
	}
}

func TestVerifyTunConnectivityRunsScopedDNSBeforeSystemResolver(t *testing.T) {
	originalRouteLookup := lookupTunRouteForProbe
	originalDial := dialTunProbeTarget
	originalScoped := resolveTunDNSNameScoped
	originalResolve := resolveTunDNSName
	defer func() {
		lookupTunRouteForProbe = originalRouteLookup
		dialTunProbeTarget = originalDial
		resolveTunDNSNameScoped = originalScoped
		resolveTunDNSName = originalResolve
	}()

	var order []string
	lookupTunRouteForProbe = func(_ context.Context, host, _ string) error {
		order = append(order, "route:"+host)
		return nil
	}
	dialTunProbeTarget = func(context.Context, string, uint16) error {
		order = append(order, "tcp")
		return nil
	}
	resolveTunDNSNameScoped = func(_ context.Context, address planner.TunAddressPlan, name string) ([]string, error) {
		order = append(order, "scoped:"+address.Interface+":"+name)
		return []string{"198.51.100.30"}, nil
	}
	resolveTunDNSName = func(_ context.Context, name string) ([]string, error) {
		order = append(order, "system:"+name)
		return []string{"198.51.100.31"}, nil
	}

	if err := verifyTunConnectivity(context.Background(), addressedTunPlanForConnectivityTest(), tunCoreRuntimePlan{}); err != nil {
		t.Fatalf("expected layered DNS verification to pass, got %v", err)
	}
	got := strings.Join(order, ",")
	want := "route:1.1.1.1,tcp,scoped:podlaz0:example.com,system:example.com,route:198.51.100.31"
	if got != want {
		t.Fatalf("unexpected verification order: got %q want %q", got, want)
	}
}

func TestVerifyTunConnectivityClassifiesScopedDNSFailure(t *testing.T) {
	originalRouteLookup := lookupTunRouteForProbe
	originalDial := dialTunProbeTarget
	originalScoped := resolveTunDNSNameScoped
	originalResolve := resolveTunDNSName
	defer func() {
		lookupTunRouteForProbe = originalRouteLookup
		dialTunProbeTarget = originalDial
		resolveTunDNSNameScoped = originalScoped
		resolveTunDNSName = originalResolve
	}()
	lookupTunRouteForProbe = func(context.Context, string, string) error { return nil }
	dialTunProbeTarget = func(context.Context, string, uint16) error { return nil }
	resolveTunDNSNameScoped = func(context.Context, planner.TunAddressPlan, string) ([]string, error) {
		return nil, errors.New("scoped timeout")
	}
	resolveTunDNSName = func(context.Context, string) ([]string, error) {
		t.Fatal("system resolver must not run after scoped DNS failure")
		return nil, nil
	}

	err := verifyTunConnectivity(context.Background(), addressedTunPlanForConnectivityTest(), tunCoreRuntimePlan{})
	if !errors.Is(err, netexecutor.ErrResolvedLinkQueryFailure) {
		t.Fatalf("expected scoped DNS classification, got %v", err)
	}
}

func TestVerifyTunConnectivityClassifiesSystemResolverFailureSeparately(t *testing.T) {
	originalRouteLookup := lookupTunRouteForProbe
	originalDial := dialTunProbeTarget
	originalScoped := resolveTunDNSNameScoped
	originalResolve := resolveTunDNSName
	defer func() {
		lookupTunRouteForProbe = originalRouteLookup
		dialTunProbeTarget = originalDial
		resolveTunDNSNameScoped = originalScoped
		resolveTunDNSName = originalResolve
	}()
	lookupTunRouteForProbe = func(context.Context, string, string) error { return nil }
	dialTunProbeTarget = func(context.Context, string, uint16) error { return nil }
	resolveTunDNSNameScoped = func(context.Context, planner.TunAddressPlan, string) ([]string, error) {
		return []string{"198.51.100.30"}, nil
	}
	resolveTunDNSName = func(context.Context, string) ([]string, error) { return nil, errors.New("system resolver timeout") }

	err := verifyTunConnectivity(context.Background(), addressedTunPlanForConnectivityTest(), tunCoreRuntimePlan{})
	if !errors.Is(err, errSystemResolverFailure) {
		t.Fatalf("expected system resolver classification, got %v", err)
	}
}

func addressedTunPlanForConnectivityTest() planner.TunPlan {
	return planner.TunPlan{
		TunDevice: planner.TunDevicePlan{Name: "podlaz0"},
		TunAddress: planner.TunAddressPlan{
			Family:            "ipv4",
			Interface:         "podlaz0",
			CIDR:              planner.DefaultTunIPv4CIDR,
			Scope:             "global",
			Action:            planner.TunAddressActionAssign,
			Owner:             netexecutor.OwnerTunAddress,
			RollbackKey:       "podlaz0/" + planner.DefaultTunIPv4CIDR,
			LinkIndex:         7,
			LinkKind:          "tun",
			AppearedAfterCore: true,
		},
	}
}

func TestVerifyTunConnectivityAcceptsAnyReturnedIPv4UsingTunRoute(t *testing.T) {
	stubScopedDNSForConnectivityTest(t)
	originalRouteLookup := lookupTunRouteForProbe
	originalDial := dialTunProbeTarget
	originalResolve := resolveTunDNSName
	defer func() {
		lookupTunRouteForProbe = originalRouteLookup
		dialTunProbeTarget = originalDial
		resolveTunDNSName = originalResolve
	}()
	var routeHosts []string
	lookupTunRouteForProbe = func(_ context.Context, host, _ string) error {
		routeHosts = append(routeHosts, host)
		if host == "198.51.100.10" {
			return errors.New("main-table exception")
		}
		return nil
	}
	dialTunProbeTarget = func(context.Context, string, uint16) error { return nil }
	resolveTunDNSName = func(context.Context, string) ([]string, error) {
		return []string{"198.51.100.10", "198.51.100.20", "198.51.100.20"}, nil
	}
	if err := verifyTunConnectivity(context.Background(), addressedTunPlanForConnectivityTest(), tunCoreRuntimePlan{}); err != nil {
		t.Fatalf("at least one returned IPv4 used the TUN route: %v", err)
	}
	if len(routeHosts) != 3 || routeHosts[1] != "198.51.100.10" || routeHosts[2] != "198.51.100.20" {
		t.Fatalf("unexpected bounded route attempts: %#v", routeHosts)
	}
}

func TestBoundedUniqueIPv4DeduplicatesAndLimitsMultiAResults(t *testing.T) {
	inputs := []net.IPAddr{{IP: net.ParseIP("2001:db8::1")}}
	for i := 1; i <= 20; i++ {
		value := net.ParseIP(fmt.Sprintf("198.51.100.%d", i))
		inputs = append(inputs, net.IPAddr{IP: value}, net.IPAddr{IP: value})
	}
	got := boundedUniqueIPv4(inputs, 16)
	if len(got) != 16 {
		t.Fatalf("bounded IPv4 result count = %d, want 16: %#v", len(got), got)
	}
	for i, value := range got {
		want := fmt.Sprintf("198.51.100.%d", i+1)
		if value != want {
			t.Fatalf("bounded IPv4 result[%d] = %q, want %q", i, value, want)
		}
	}
}

func TestVerifyAnyResolvedIPv4UsesFairBoundedAttempts(t *testing.T) {
	originalRouteLookup := lookupTunRouteForProbe
	defer func() { lookupTunRouteForProbe = originalRouteLookup }()
	var attempts []string
	lookupTunRouteForProbe = func(ctx context.Context, host, _ string) error {
		attempts = append(attempts, host)
		if host == "198.51.100.10" {
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	if err := verifyAnyResolvedIPv4UsesTunRoute(ctx, []string{"198.51.100.10", "198.51.100.20"}, "podlaz0"); err != nil {
		t.Fatalf("later IPv4 was not checked after bounded first-address timeout: %v", err)
	}
	if len(attempts) != 2 || attempts[1] != "198.51.100.20" {
		t.Fatalf("unexpected fair route attempts: %#v", attempts)
	}
}
