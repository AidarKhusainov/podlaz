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

	"github.com/AidarKhusainov/tunwarden/internal/network/planner"
)

const (
	defaultTunProbeHost = "1.1.1.1"
	defaultTunProbePort = uint16(53)
	defaultProbeTimeout = 3 * time.Second
)

type tunRouteLookupFunc func(context.Context, string, string) error
type tunTCPProbeFunc func(context.Context, string, uint16) error

var (
	lookupTunRouteForProbe = defaultLookupTunRouteForProbe
	dialTunProbeTarget     = defaultDialTunProbeTarget
)

func verifyTunConnectivity(ctx context.Context, plan planner.TunPlan, core tunCoreRuntimePlan) error {
	_ = core
	if plan.TunDevice.Name == "" {
		return errors.New("connectivity probe requires a planned TUN device")
	}
	probeHost := selectTunProbeHost(plan)
	probeCtx, cancel := context.WithTimeout(ctx, defaultProbeTimeout)
	defer cancel()
	if err := lookupTunRouteForProbe(probeCtx, probeHost, plan.TunDevice.Name); err != nil {
		return fmt.Errorf("full-tunnel route lookup for %s failed: %w", probeHost, err)
	}
	if err := dialTunProbeTarget(probeCtx, probeHost, defaultTunProbePort); err != nil {
		return fmt.Errorf("basic full-tunnel connectivity probe to %s:%d failed: %w", probeHost, defaultTunProbePort, err)
	}
	return nil
}

func selectTunProbeHost(plan planner.TunPlan) string {
	serverBypassCIDR := strings.TrimSpace(plan.ServerBypass.Destination)
	if strings.HasPrefix(serverBypassCIDR, defaultTunProbeHost+"/") {
		return "1.0.0.1"
	}
	return defaultTunProbeHost
}

func defaultLookupTunRouteForProbe(ctx context.Context, host, tunDevice string) error {
	cmd := exec.CommandContext(ctx, "ip", "-4", "route", "get", host)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ip -4 route get %s: %w: %s", host, err, strings.TrimSpace(string(output)))
	}
	line := strings.TrimSpace(string(output))
	fields := strings.Fields(line)
	if !containsAdjacentRouteFields(fields, "dev", tunDevice) {
		return fmt.Errorf("route lookup did not use TUN device %s: %s", tunDevice, line)
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

func containsAdjacentRouteFields(fields []string, key, value string) bool {
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == key && fields[i+1] == value {
			return true
		}
	}
	return false
}
