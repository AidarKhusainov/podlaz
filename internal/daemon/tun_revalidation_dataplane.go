package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
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
	tunRevalidationGoogleHTTPSTargetID = "https-google-small"
)

type tunRevalidationNetworkClient interface {
	DNSUDP(context.Context, string, string, uint16) (tundiag.DNSEvidence, error)
	DNSTCP(context.Context, string, string, uint16) (tundiag.DNSEvidence, error)
	TCP(context.Context, string, uint16) (time.Duration, error)
	TLS(context.Context, string, uint16) (tundiag.TLSEvidence, error)
	HTTPS(context.Context, tundiag.Target) (tundiag.HTTPEvidence, error)
}

func newTunRevalidationNetworkClient() tundiag.NetworkClient {
	dialer := &net.Dialer{}
	return tundiag.NetworkClient{DialContext: tunRevalidationCancellableDial(dialer.DialContext)}
}

func tunRevalidationCancellableDial(base tundiag.DialContextFunc) tundiag.DialContextFunc {
	if base == nil {
		dialer := &net.Dialer{}
		base = dialer.DialContext
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := base(ctx, network, address)
		if err != nil {
			return nil, err
		}
		wrapped := &tunRevalidationContextConn{Conn: conn, ctx: ctx}
		wrapped.stop = context.AfterFunc(ctx, func() { _ = conn.Close() })
		if err := ctx.Err(); err != nil {
			_ = wrapped.Close()
			return nil, err
		}
		return wrapped, nil
	}
}

type tunRevalidationContextConn struct {
	net.Conn
	ctx  context.Context
	stop func() bool
}

func (c *tunRevalidationContextConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	return n, tunRevalidationContextIOError(c.ctx, err)
}

func (c *tunRevalidationContextConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	return n, tunRevalidationContextIOError(c.ctx, err)
}

func tunRevalidationContextIOError(ctx context.Context, err error) error {
	if err == nil || ctx == nil {
		return err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	deadline, hasDeadline := ctx.Deadline()
	if !hasDeadline || time.Now().Before(deadline) {
		return err
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return context.DeadlineExceeded
	}
	return err
}

func (c *tunRevalidationContextConn) Close() error {
	if c.stop != nil {
		c.stop()
	}
	return c.Conn.Close()
}

func verifyTunRevalidationDataPlane(ctx context.Context, plan planner.TunPlan, client tunRevalidationNetworkClient) error {
	if client == nil {
		client = newTunRevalidationNetworkClient()
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

// collectTunRevalidationProbeEvidence collects soft data-plane evidence without
// making a lifecycle disposition. Ordinary probe failures are retained as typed
// evidence and do not prevent independent providers from being sampled. Context
// cancellation/deadline still aborts the round immediately.
func collectTunRevalidationProbeEvidence(ctx context.Context, plan planner.TunPlan, client tunRevalidationNetworkClient) ([]tunProbeEvidence, error) {
	if client == nil {
		client = newTunRevalidationNetworkClient()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	dnsTarget, err := tunRevalidationTarget(tunRevalidationDNSPositiveTargetID, tundiag.TargetDNS)
	if err != nil {
		return nil, err
	}
	server, err := tunRevalidationDNSServer(plan)
	if err != nil {
		return nil, err
	}
	tcpTarget, err := tunRevalidationTarget(tunRevalidationTCP443TargetID, tundiag.TargetTCP)
	if err != nil {
		return nil, err
	}
	tlsTarget, err := tunRevalidationTarget(tunRevalidationTLSTargetID, tundiag.TargetTLS)
	if err != nil {
		return nil, err
	}
	cloudflareHTTPS, err := tunRevalidationTarget(tunRevalidationHTTPSTargetID, tundiag.TargetHTTPS)
	if err != nil {
		return nil, err
	}
	googleHTTPS, err := tunRevalidationTarget(tunRevalidationGoogleHTTPSTargetID, tundiag.TargetHTTPS)
	if err != nil {
		return nil, err
	}

	out := make([]tunProbeEvidence, 0, 6)
	appendProbe := func(group, provider string, timeout time.Duration, probe func(context.Context) error) error {
		probeErr := runProbe(ctx, timeout, probe)
		if errors.Is(probeErr, context.Canceled) || errors.Is(probeErr, context.DeadlineExceeded) {
			return probeErr
		}
		out = append(out, tunProbeEvidence{Group: group, Provider: provider, Success: probeErr == nil, Cause: probeErr})
		return nil
	}

	var udpEvidence tundiag.DNSEvidence
	if err := appendProbe("dns-udp", "session-resolver", dnsTarget.Timeout, func(probeCtx context.Context) error {
		var callErr error
		udpEvidence, callErr = client.DNSUDP(probeCtx, server, dnsTarget.Host, tundiag.DNSRecordTypeA)
		if callErr != nil {
			return callErr
		}
		return validateTunRevalidationDNSResponse(udpEvidence)
	}); err != nil {
		return nil, err
	}

	var tcpDNSEvidence tundiag.DNSEvidence
	if err := appendProbe("dns-tcp", "session-resolver", dnsTarget.Timeout, func(probeCtx context.Context) error {
		var callErr error
		tcpDNSEvidence, callErr = client.DNSTCP(probeCtx, server, dnsTarget.Host, tundiag.DNSRecordTypeA)
		if callErr != nil {
			return callErr
		}
		return validateTunRevalidationDNSResponse(tcpDNSEvidence)
	}); err != nil {
		return nil, err
	}

	if err := appendProbe("tcp", "cloudflare", tcpTarget.Timeout, func(probeCtx context.Context) error {
		_, callErr := client.TCP(probeCtx, tcpTarget.Host, tcpTarget.Port)
		return callErr
	}); err != nil {
		return nil, err
	}
	if err := appendProbe("tls", "cloudflare", tlsTarget.Timeout, func(probeCtx context.Context) error {
		_, callErr := client.TLS(probeCtx, tlsTarget.Host, tlsTarget.Port)
		return callErr
	}); err != nil {
		return nil, err
	}
	if err := appendProbe("https", "cloudflare", cloudflareHTTPS.Timeout, func(probeCtx context.Context) error {
		_, callErr := client.HTTPS(probeCtx, cloudflareHTTPS)
		return callErr
	}); err != nil {
		return nil, err
	}
	if err := appendProbe("https", "google", googleHTTPS.Timeout, func(probeCtx context.Context) error {
		_, callErr := client.HTTPS(probeCtx, googleHTTPS)
		return callErr
	}); err != nil {
		return nil, err
	}
	return out, nil
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
