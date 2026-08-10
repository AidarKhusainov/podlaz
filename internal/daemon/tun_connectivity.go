package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

const (
	defaultTunProbeHost          = "1.1.1.1"
	defaultTunAlternateProbeHost = "1.0.0.1"
	defaultTunProbePort          = uint16(53)
	defaultTunDNSProbeName       = "example.com"
	routeProbeTimeout            = 3 * time.Second
	tcpProbeTimeout              = 3 * time.Second
	dnsProbeTimeout              = 15 * time.Second
	diagnosticTimeout            = 8 * time.Second
	commandTimeout               = 3 * time.Second
)

type tunRouteLookupFunc func(context.Context, string, string) error
type tunTCPProbeFunc func(context.Context, string, uint16) error
type tunDNSResolveFunc func(context.Context, string) ([]string, error)
type tunScopedDNSResolveFunc func(context.Context, planner.TunAddressPlan, string) ([]string, error)

type tunConnectivityProbeConfig struct {
	RouteHost     string
	AlternateHost string
	TCPPort       uint16
	DNSName       string
	RouteTimeout  time.Duration
	TCPTimeout    time.Duration
	DNSTimeout    time.Duration
}

var (
	errSystemResolverFailure = errors.New("system resolver failed")

	lookupTunRouteForProbe       = defaultLookupTunRouteForProbe
	dialTunProbeTarget           = defaultDialTunProbeTarget
	resolveTunDNSNameScoped      = defaultResolveTunDNSNameScoped
	resolveTunDNSName            = defaultResolveTunDNSName
	diagnosticDomainTokenPattern = regexp.MustCompile(`\b(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+(?:[A-Za-z]{2,63}|test)\b`)
)

func verifyTunConnectivity(ctx context.Context, plan planner.TunPlan, core tunCoreRuntimePlan) error {
	if plan.TunDevice.Name == "" {
		return errors.New("connectivity probe requires a planned TUN device")
	}
	probe := core.ConnectivityProbe.withDefaults()
	probeHost := selectTunProbeHostWithConfig(plan, probe)
	if err := runProbe(ctx, probe.RouteTimeout, func(probeCtx context.Context) error {
		return lookupTunRouteForProbe(probeCtx, probeHost, plan.TunDevice.Name)
	}); err != nil {
		return newTunVerificationError("route", fmt.Sprintf("Full-tunnel route lookup for %s did not use the planned TUN path", probeHost), err)
	}
	if err := runProbe(ctx, probe.TCPTimeout, func(probeCtx context.Context) error {
		return dialTunProbeTarget(probeCtx, probeHost, probe.TCPPort)
	}); err != nil {
		return newTunVerificationError("tcp", fmt.Sprintf("Basic full-tunnel connectivity probe to %s:%d failed", probeHost, probe.TCPPort), err)
	}
	if err := validateResolvedLinkReadiness(plan); err != nil {
		return newTunVerificationError("resolved-link", "The planned TUN link is not ready for functional DNS verification", errors.Join(netexecutor.ErrResolvedLinkNotReady, err))
	}
	if err := runProbe(ctx, probe.DNSTimeout, func(probeCtx context.Context) error {
		_, err := resolveTunDNSNameScoped(probeCtx, plan.TunAddress, probe.DNSName)
		return err
	}); err != nil {
		if errors.Is(err, netexecutor.ErrResolvedLinkNotReady) {
			return newTunVerificationError("resolved-link", "The planned TUN link is not ready for functional DNS verification", err)
		}
		return newTunVerificationError("resolved-link-query", fmt.Sprintf("Uncached DNS query for %s through %s failed", probe.DNSName, plan.TunDevice.Name), errors.Join(netexecutor.ErrResolvedLinkQueryFailure, err))
	}
	var resolvedIPs []string
	if err := runProbe(ctx, probe.DNSTimeout, func(probeCtx context.Context) error {
		ips, err := resolveTunDNSName(probeCtx, probe.DNSName)
		resolvedIPs = ips
		return err
	}); err != nil {
		return newTunVerificationError("system-resolver", fmt.Sprintf("The system resolver did not resolve %s through the active TUN path", probe.DNSName), errors.Join(errSystemResolverFailure, err))
	}
	if err := runProbe(ctx, probe.RouteTimeout, func(probeCtx context.Context) error {
		return verifyAnyResolvedIPv4UsesTunRoute(probeCtx, resolvedIPs, plan.TunDevice.Name)
	}); err != nil {
		return newTunVerificationError("dns-route", fmt.Sprintf("No IPv4 result for %s used the planned TUN path", probe.DNSName), err)
	}
	return nil
}

func verifyAnyResolvedIPv4UsesTunRoute(ctx context.Context, resolvedIPs []string, tunDevice string) error {
	if len(resolvedIPs) == 0 {
		return errors.New("system resolver returned no IPv4 results")
	}
	var routeErrors []error
	for i, resolvedIP := range resolvedIPs {
		attemptCtx := ctx
		cancel := func() {}
		if deadline, ok := ctx.Deadline(); ok {
			remaining := time.Until(deadline)
			remainingAttempts := len(resolvedIPs) - i
			if remaining <= 0 {
				routeErrors = append(routeErrors, context.DeadlineExceeded)
				break
			}
			attemptBudget := remaining / time.Duration(remainingAttempts)
			if attemptBudget <= 0 {
				attemptBudget = remaining
			}
			attemptCtx, cancel = context.WithTimeout(ctx, attemptBudget)
		}
		err := lookupTunRouteForProbe(attemptCtx, resolvedIP, tunDevice)
		cancel()
		if err == nil {
			return nil
		}
		routeErrors = append(routeErrors, fmt.Errorf("%s: %w", resolvedIP, err))
	}
	return errors.Join(routeErrors...)
}

func (c tunConnectivityProbeConfig) withDefaults() tunConnectivityProbeConfig {
	if strings.TrimSpace(c.RouteHost) == "" {
		c.RouteHost = defaultTunProbeHost
	}
	if strings.TrimSpace(c.AlternateHost) == "" {
		c.AlternateHost = defaultTunAlternateProbeHost
	}
	if c.TCPPort == 0 {
		c.TCPPort = defaultTunProbePort
	}
	if strings.TrimSpace(c.DNSName) == "" {
		c.DNSName = defaultTunDNSProbeName
	}
	if c.RouteTimeout <= 0 {
		c.RouteTimeout = routeProbeTimeout
	}
	if c.TCPTimeout <= 0 {
		c.TCPTimeout = tcpProbeTimeout
	}
	if c.DNSTimeout <= 0 {
		c.DNSTimeout = dnsProbeTimeout
	}
	return c
}

func runProbe(ctx context.Context, timeout time.Duration, fn func(context.Context) error) error {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return fn(probeCtx)
}

func selectTunProbeHost(plan planner.TunPlan) string {
	return selectTunProbeHostWithConfig(plan, tunConnectivityProbeConfig{}.withDefaults())
}

func selectTunProbeHostWithConfig(plan planner.TunPlan, probe tunConnectivityProbeConfig) string {
	probe = probe.withDefaults()
	serverBypassCIDR := strings.TrimSpace(plan.ServerBypass.Destination)
	if strings.HasPrefix(serverBypassCIDR, probe.RouteHost+"/") {
		return probe.AlternateHost
	}
	return probe.RouteHost
}

func tunRouteLookupCommand(host string) (string, []string) {
	return "ip", []string{"-4", "route", "get", host}
}

func defaultLookupTunRouteForProbe(ctx context.Context, host, tunDevice string) error {
	name, args := tunRouteLookupCommand(host)
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ip -4 route get %s: %w: %s%s", host, err, sanitizeConnectivityDiagnostic(string(output)), tunRouteDiagnostics(host, tunDevice))
	}
	line := strings.TrimSpace(string(output))
	fields := strings.Fields(line)
	if !containsAdjacentRouteFields(fields, "dev", tunDevice) {
		return fmt.Errorf("route lookup did not use TUN device %s: %s%s", tunDevice, sanitizeConnectivityDiagnostic(line), tunRouteDiagnostics(host, tunDevice))
	}
	return nil
}

func defaultDialTunProbeTarget(ctx context.Context, host string, port uint16) error {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		return err
	}
	return conn.Close()
}

func validateResolvedLinkReadiness(plan planner.TunPlan) error {
	address := plan.TunAddress
	if strings.TrimSpace(plan.TunDevice.Name) == "" || strings.TrimSpace(address.Interface) != strings.TrimSpace(plan.TunDevice.Name) {
		return errors.New("planned TUN address interface does not match the TUN device")
	}
	if address.Family != "ipv4" || strings.TrimSpace(address.CIDR) == "" || address.Action != planner.TunAddressActionAssign {
		return errors.New("planned daemon-owned IPv4 address is absent")
	}
	if address.LinkIndex <= 0 || address.LinkKind != "tun" || !address.AppearedAfterCore {
		return errors.New("Xray-created TUN link identity is incomplete")
	}
	if address.Owner != netexecutor.OwnerTunAddress {
		return errors.New("daemon-owned TUN address ownership is missing")
	}
	return nil
}

func defaultResolveTunDNSNameScoped(ctx context.Context, address planner.TunAddressPlan, name string) ([]string, error) {
	return (netexecutor.TunDNSReadinessVerifier{Runner: netexecutor.OSRunner{}}).VerifyScoped(ctx, address, name)
}

func defaultResolveTunDNSName(ctx context.Context, name string) ([]string, error) {
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", name, err)
	}
	const maxResolvedIPv4 = 16
	resolved := boundedUniqueIPv4(ips, maxResolvedIPv4)
	if len(resolved) == 0 {
		return nil, fmt.Errorf("resolve %s returned no IPv4 address: %v", name, ips)
	}
	return resolved, nil
}

func boundedUniqueIPv4(ips []net.IPAddr, limit int) []string {
	if limit <= 0 {
		return nil
	}
	seen := make(map[string]struct{}, limit)
	resolved := make([]string, 0, limit)
	for _, ip := range ips {
		ipv4 := ip.IP.To4()
		if ipv4 == nil {
			continue
		}
		value := ipv4.String()
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		resolved = append(resolved, value)
		if len(resolved) == limit {
			break
		}
	}
	return resolved
}

func containsAdjacentRouteFields(fields []string, key, value string) bool {
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == key && fields[i+1] == value {
			return true
		}
	}
	return false
}

func tunRouteDiagnostics(host, tunDevice string) string {
	checks := []struct {
		label string
		args  []string
	}{
		{label: "ip -4 rule show", args: []string{"-4", "rule", "show"}},
		{label: "ip -4 route show table 51820", args: []string{"-4", "route", "show", "table", strconv.Itoa(planner.TunRoutingTableID)}},
		{label: "ip -4 route get table 51820", args: []string{"-4", "route", "get", host, "table", strconv.Itoa(planner.TunRoutingTableID)}},
		{label: "ip -4 addr show dev " + tunDevice, args: []string{"-4", "addr", "show", "dev", tunDevice}},
		{label: "ip -4 link show dev " + tunDevice, args: []string{"-4", "link", "show", "dev", tunDevice}},
	}

	var builder strings.Builder
	builder.WriteString("; diagnostics:")
	for _, check := range checks {
		builder.WriteString("\n")
		builder.WriteString(check.label)
		builder.WriteString(": ")
		builder.WriteString(runLiveDiagnosticCommand("ip", check.args...))
	}
	return builder.String()
}

func tunDNSDiagnostics(name string) string {
	checks := []struct {
		label string
		name  string
		args  []string
	}{
		{label: "resolvectl query " + name, name: "resolvectl", args: []string{"query", name}},
		{label: "getent ahostsv4 " + name, name: "getent", args: []string{"ahostsv4", name}},
		{label: "resolvectl status", name: "resolvectl", args: []string{"status", "--no-pager"}},
	}

	var builder strings.Builder
	builder.WriteString("; DNS diagnostics:")
	for _, check := range checks {
		builder.WriteString("\n")
		builder.WriteString(check.label)
		builder.WriteString(": ")
		builder.WriteString(runLiveDiagnosticCommand(check.name, check.args...))
	}
	return builder.String()
}

func runLiveDiagnosticCommand(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), diagnosticTimeout)
	defer cancel()
	return runDiagnosticCommand(ctx, name, args...)
}

func runDiagnosticCommand(ctx context.Context, name string, args ...string) string {
	cmdCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, name, args...)
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		if text == "" {
			return sanitizeConnectivityDiagnostic(err.Error())
		}
		return sanitizeConnectivityDiagnostic(err.Error() + ": " + text)
	}
	if text == "" {
		return "<empty>"
	}
	return sanitizeConnectivityDiagnostic(text)
}

func sanitizeConnectivityDiagnostic(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	text = safeDiagnosticValue(text)
	if text == "" || text == "<missing>" {
		return text
	}
	return diagnosticDomainTokenPattern.ReplaceAllStringFunc(text, func(candidate string) string {
		lower := strings.ToLower(candidate)
		switch {
		case lower == "example.com", strings.HasSuffix(lower, ".example.com"), strings.HasSuffix(lower, ".example.test"):
			return candidate
		default:
			return "<domain>"
		}
	})
}
