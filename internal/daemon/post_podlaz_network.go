package daemon

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

type postPodlazNetworkVerifier struct {
	collect     func(context.Context) netsnapshot.Snapshot
	verifyRoute func(context.Context, string) error
	verifyTCP   tunTCPProbeFunc
	verifyDNS   tunDNSResolveFunc
}

func newPostPodlazNetworkVerifier() postPodlazNetworkVerifier {
	return postPodlazNetworkVerifier{
		collect: func(ctx context.Context) netsnapshot.Snapshot {
			return netsnapshot.Collect(ctx, netsnapshot.Options{})
		},
		verifyRoute: defaultVerifyPostPodlazRoute,
		verifyTCP:   defaultDialTunProbeTarget,
		verifyDNS:   defaultResolveTunDNSName,
	}
}

func (v postPodlazNetworkVerifier) Verify(ctx context.Context) error {
	if v.collect == nil || v.verifyRoute == nil || v.verifyTCP == nil || v.verifyDNS == nil {
		return errors.New("post-Podlaz network verifier is incomplete")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	snapshot := v.collect(ctx)
	if snapshot.DefaultIPv4.Status != netsnapshot.StatusDetected {
		return fmt.Errorf("remaining host default IPv4 route is %s", snapshot.DefaultIPv4.Status)
	}
	iface := strings.TrimSpace(snapshot.DefaultIPv4.Interface)
	if iface == "" {
		return errors.New("remaining host default IPv4 route has no interface")
	}
	if iface == netsnapshot.DefaultTunName {
		return errors.New("remaining host default IPv4 route still uses the Podlaz TUN interface")
	}

	if err := runProbe(ctx, routeProbeTimeout, func(probeCtx context.Context) error {
		return v.verifyRoute(probeCtx, defaultTunProbeHost)
	}); err != nil {
		return fmt.Errorf("verify remaining host route: %w", err)
	}
	if err := runProbe(ctx, tcpProbeTimeout, func(probeCtx context.Context) error {
		return v.verifyTCP(probeCtx, defaultTunProbeHost, defaultTunProbePort)
	}); err != nil {
		return fmt.Errorf("verify remaining host TCP connectivity: %w", err)
	}
	if err := runProbe(ctx, dnsProbeTimeout, func(probeCtx context.Context) error {
		resolved, err := v.verifyDNS(probeCtx, defaultTunDNSProbeName)
		if err != nil {
			return err
		}
		if len(resolved) == 0 {
			return errors.New("system resolver returned no IPv4 results")
		}
		return nil
	}); err != nil {
		return fmt.Errorf("verify remaining host DNS connectivity: %w", err)
	}
	return nil
}

func defaultVerifyPostPodlazRoute(ctx context.Context, host string) error {
	cmd := exec.CommandContext(ctx, "ip", "-4", "route", "get", host)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ip -4 route get: %w: %s", err, sanitizeConnectivityDiagnostic(string(output)))
	}
	fields := strings.Fields(string(output))
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] != "dev" {
			continue
		}
		iface := strings.TrimSpace(fields[i+1])
		if iface == "" {
			return errors.New("route lookup returned an empty interface")
		}
		if iface == netsnapshot.DefaultTunName {
			return errors.New("route lookup still uses the Podlaz TUN interface")
		}
		return nil
	}
	return errors.New("route lookup did not expose an output interface")
}
