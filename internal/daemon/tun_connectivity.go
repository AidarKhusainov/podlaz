package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

const (
	defaultTunProbeHost    = "1.1.1.1"
	defaultTunProbePort    = uint16(53)
	defaultTunDNSProbeName = "example.com"
	routeProbeTimeout      = 3 * time.Second
	tcpProbeTimeout        = 3 * time.Second
	dnsProbeTimeout        = 15 * time.Second
	diagnosticTimeout      = 8 * time.Second
	commandTimeout         = 3 * time.Second
)

type tunRouteLookupFunc func(context.Context, string, string) error
type tunTCPProbeFunc func(context.Context, string, uint16) error
type tunDNSResolveFunc func(context.Context, string) (string, error)

var (
	lookupTunRouteForProbe = defaultLookupTunRouteForProbe
	dialTunProbeTarget     = defaultDialTunProbeTarget
	resolveTunDNSName      = defaultResolveTunDNSName
)

func verifyTunConnectivity(ctx context.Context, plan planner.TunPlan, core tunCoreRuntimePlan) error {
	_ = core
	if plan.TunDevice.Name == "" {
		return errors.New("connectivity probe requires a planned TUN device")
	}
	probeHost := selectTunProbeHost(plan)
	if err := runProbe(ctx, routeProbeTimeout, func(probeCtx context.Context) error {
		return lookupTunRouteForProbe(probeCtx, probeHost, plan.TunDevice.Name)
	}); err != nil {
		return fmt.Errorf("full-tunnel route lookup for %s failed: %w", probeHost, err)
	}
	if err := runProbe(ctx, tcpProbeTimeout, func(probeCtx context.Context) error {
		return dialTunProbeTarget(probeCtx, probeHost, defaultTunProbePort)
	}); err != nil {
		return fmt.Errorf("basic full-tunnel connectivity probe to %s:%d failed: %w", probeHost, defaultTunProbePort, err)
	}
	var resolvedIP string
	if err := runProbe(ctx, dnsProbeTimeout, func(probeCtx context.Context) error {
		ip, err := resolveTunDNSName(probeCtx, defaultTunDNSProbeName)
		resolvedIP = ip
		return err
	}); err != nil {
		return fmt.Errorf("full-tunnel DNS probe for %s failed: %w", defaultTunDNSProbeName, err)
	}
	if err := runProbe(ctx, routeProbeTimeout, func(probeCtx context.Context) error {
		return lookupTunRouteForProbe(probeCtx, resolvedIP, plan.TunDevice.Name)
	}); err != nil {
		return fmt.Errorf("full-tunnel route lookup for DNS result %s (%s) failed: %w", defaultTunDNSProbeName, resolvedIP, err)
	}
	return nil
}

func runProbe(ctx context.Context, timeout time.Duration, fn func(context.Context) error) error {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return fn(probeCtx)
}

func selectTunProbeHost(plan planner.TunPlan) string {
	serverBypassCIDR := strings.TrimSpace(plan.ServerBypass.Destination)
	if strings.HasPrefix(serverBypassCIDR, defaultTunProbeHost+"/") {
		return "1.0.0.1"
	}
	return defaultTunProbeHost
}

func defaultLookupTunRouteForProbe(ctx context.Context, host, tunDevice string) error {
	_ = runDiagnosticCommand(ctx, "ip", "-4", "route", "flush", "cache")
	cmd := exec.CommandContext(ctx, "ip", "-4", "route", "get", host)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ip -4 route get %s: %w: %s%s", host, err, strings.TrimSpace(string(output)), tunRouteDiagnostics(host, tunDevice))
	}
	line := strings.TrimSpace(string(output))
	fields := strings.Fields(line)
	if !containsAdjacentRouteFields(fields, "dev", tunDevice) {
		return fmt.Errorf("route lookup did not use TUN device %s: %s%s", tunDevice, line, tunRouteDiagnostics(host, tunDevice))
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

func defaultResolveTunDNSName(ctx context.Context, name string) (string, error) {
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, name)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w%s", name, err, tunDNSDiagnostics(name))
	}
	for _, ip := range ips {
		if ipv4 := ip.IP.To4(); ipv4 != nil {
			return ipv4.String(), nil
		}
	}
	return "", fmt.Errorf("resolve %s returned no IPv4 address: %v%s", name, ips, tunDNSDiagnostics(name))
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
			return err.Error()
		}
		return err.Error() + ": " + text
	}
	if text == "" {
		return "<empty>"
	}
	return text
}
