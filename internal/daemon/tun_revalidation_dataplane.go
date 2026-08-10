package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

const (
	tunRevalidationDNSPositiveTargetID = "dns-system-positive"
	tunRevalidationTCP443TargetID      = "tcp-443-cloudflare"
	tunRevalidationTLSTargetID         = "tls-cloudflare"
	tunRevalidationHTTPSTargetID       = "https-cloudflare-small"
)

type tunRevalidationNetworkClient interface {
	DNSUDP(context.Context, string, string, uint16) (tundiag.DNSEvidence, error)
	DNSTCP(context.Context, string, string, uint16) (tundiag.DNSEvidence, error)
	TCP(context.Context, string, uint16) (time.Duration, error)
	TLS(context.Context, string, uint16) (tundiag.TLSEvidence, error)
	HTTPS(context.Context, tundiag.Target) (tundiag.HTTPEvidence, error)
}

func verifyTunRevalidationDataPlane(ctx context.Context, plan planner.TunPlan, client tunRevalidationNetworkClient) error {
	if client == nil {
		client = tundiag.NetworkClient{}
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	dnsTarget, err := tunRevalidationTarget(tunRevalidationDNSPositiveTargetID, tundiag.TargetDNS)
	if err != nil {
		return err
	}
	server, err := tunRevalidationDNSServer(plan)
	if err != nil {
		return newTunVerificationError("dns-udp", "Layered revalidation requires a planned DNS server", err)
	}

	var udpEvidence tundiag.DNSEvidence
	if err := runProbe(ctx, dnsTarget.Timeout, func(probeCtx context.Context) error {
		udpEvidence, err = client.DNSUDP(probeCtx, server, dnsTarget.Host, tundiag.DNSRecordTypeA)
		return err
	}); err != nil {
		return newTunVerificationError("dns-udp", "Explicit DNS-over-UDP revalidation failed", err)
	}
	if err := validateTunRevalidationDNSResponse(udpEvidence); err != nil {
		return newTunVerificationError("dns-udp", "Explicit DNS-over-UDP revalidation returned unusable evidence", err)
	}

	var tcpDNSEvidence tundiag.DNSEvidence
	if err := runProbe(ctx, dnsTarget.Timeout, func(probeCtx context.Context) error {
		tcpDNSEvidence, err = client.DNSTCP(probeCtx, server, dnsTarget.Host, tundiag.DNSRecordTypeA)
		return err
	}); err != nil {
		return newTunVerificationError("dns-tcp", "Explicit DNS-over-TCP revalidation failed", err)
	}
	if err := validateTunRevalidationDNSResponse(tcpDNSEvidence); err != nil {
		return newTunVerificationError("dns-tcp", "Explicit DNS-over-TCP revalidation returned unusable evidence", err)
	}

	tcpTarget, err := tunRevalidationTarget(tunRevalidationTCP443TargetID, tundiag.TargetTCP)
	if err != nil {
		return err
	}
	if err := runProbe(ctx, tcpTarget.Timeout, func(probeCtx context.Context) error {
		_, err := client.TCP(probeCtx, tcpTarget.Host, tcpTarget.Port)
		return err
	}); err != nil {
		return newTunVerificationError("tcp-443", "TCP/443 revalidation failed", err)
	}

	tlsTarget, err := tunRevalidationTarget(tunRevalidationTLSTargetID, tundiag.TargetTLS)
	if err != nil {
		return err
	}
	if err := runProbe(ctx, tlsTarget.Timeout, func(probeCtx context.Context) error {
		_, err := client.TLS(probeCtx, tlsTarget.Host, tlsTarget.Port)
		return err
	}); err != nil {
		return newTunVerificationError("tls", "TLS revalidation failed", err)
	}

	httpsTarget, err := tunRevalidationTarget(tunRevalidationHTTPSTargetID, tundiag.TargetHTTPS)
	if err != nil {
		return err
	}
	if err := runProbe(ctx, httpsTarget.Timeout, func(probeCtx context.Context) error {
		_, err := client.HTTPS(probeCtx, httpsTarget)
		return err
	}); err != nil {
		return newTunVerificationError("https", "HTTPS revalidation failed", err)
	}
	return nil
}

func tunRevalidationTarget(id string, kind tundiag.TargetKind) (tundiag.Target, error) {
	target, ok := tundiag.FindTarget(id)
	if !ok {
		return tundiag.Target{}, fmt.Errorf("missing TUN revalidation target %s", id)
	}
	if target.Kind != kind || strings.TrimSpace(target.Host) == "" || target.Timeout <= 0 {
		return tundiag.Target{}, fmt.Errorf("invalid TUN revalidation target %s", id)
	}
	if (kind == tundiag.TargetTCP || kind == tundiag.TargetTLS) && target.Port == 0 {
		return tundiag.Target{}, fmt.Errorf("invalid TUN revalidation target %s port", id)
	}
	if kind == tundiag.TargetHTTPS && strings.TrimSpace(target.URL) == "" {
		return tundiag.Target{}, fmt.Errorf("invalid TUN revalidation target %s URL", id)
	}
	return target, nil
}

func tunRevalidationDNSServer(plan planner.TunPlan) (string, error) {
	for _, server := range plan.DNS.Servers {
		server = strings.TrimSpace(server)
		if server != "" {
			return server, nil
		}
	}
	return "", errors.New("no planned DNS server")
}

func validateTunRevalidationDNSResponse(evidence tundiag.DNSEvidence) error {
	if evidence.ResponseCode != tundiag.DNSRCodeSuccess {
		return fmt.Errorf("DNS response code=%d, want %d", evidence.ResponseCode, tundiag.DNSRCodeSuccess)
	}
	if len(evidence.Addresses) == 0 {
		return errors.New("DNS response contained no IPv4 address")
	}
	return nil
}
