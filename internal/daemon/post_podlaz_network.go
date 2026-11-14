package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"

	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

var (
	errPostPodlazVerifierIncomplete = errors.New("post-Podlaz network verifier is incomplete")
	errPostPodlazTunPath            = errors.New("remaining host route still uses the Podlaz TUN interface")
)

type postPodlazNetworkWarningSinkKey struct{}

func checkPostPodlazNetwork(ctx context.Context, verify func(context.Context) error) (string, error) {
	if verify == nil {
		return "", errPostPodlazVerifierIncomplete
	}
	start := time.Now()
	err := verify(ctx)
	if err == nil {
		return "", nil
	}
	if errors.Is(err, errPostPodlazTunPath) || errors.Is(err, errPostPodlazVerifierIncomplete) {
		return "", err
	}
	warning := fmt.Sprintf("Ordinary network check failed after %s: %s",
		time.Since(start).Round(time.Millisecond), sanitizeConnectivityDiagnostic(err.Error()))
	log.Printf("podlazd: %s", warning)
	if sink, ok := ctx.Value(postPodlazNetworkWarningSinkKey{}).(func(api.RecoveryWarning)); ok {
		sink(api.RecoveryWarning{Target: api.RecoveryWarningOrdinaryNetwork, Message: warning})
	}
	return warning, nil
}

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
		return errPostPodlazVerifierIncomplete
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
		return errPostPodlazTunPath
	}

	if err := runProbe(ctx, routeProbeTimeout, func(probeCtx context.Context) error {
		return v.verifyRoute(probeCtx, defaultTunProbeHost)
	}); err != nil {
		return fmt.Errorf("verify remaining host route: %w", err)
	}
	tcpStarted := time.Now()
	if err := runProbe(ctx, tcpProbeTimeout, func(probeCtx context.Context) error {
		return v.verifyTCP(probeCtx, defaultTunProbeHost, defaultTunProbePort)
	}); err != nil {
		return fmt.Errorf("verify remaining host TCP connectivity: %w (TCP probe elapsed %s)", err, time.Since(tcpStarted).Round(time.Microsecond))
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
			return errPostPodlazTunPath
		}
		return nil
	}
	return errors.New("route lookup did not expose an output interface")
}
