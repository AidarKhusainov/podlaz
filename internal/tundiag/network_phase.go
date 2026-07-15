package tundiag

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http/httptrace"
	"strconv"
	"sync"
	"time"
)

type failurePhaseError struct {
	phase FailurePhase
	err   error
}

func (e failurePhaseError) Error() string { return e.err.Error() }
func (e failurePhaseError) Unwrap() error { return e.err }

func withFailurePhase(phase FailurePhase, err error) error {
	if err == nil || phase == "" {
		return err
	}
	return failurePhaseError{phase: phase, err: err}
}

// FailurePhaseOf returns the stable machine-readable phase attached by the
// network diagnostic client. It intentionally does not infer phases from error
// strings.
func FailurePhaseOf(err error) FailurePhase {
	var phased failurePhaseError
	if errors.As(err, &phased) {
		return phased.phase
	}
	return ""
}

type failurePhaseTracker struct {
	mu    sync.Mutex
	phase FailurePhase
}

func (t *failurePhaseTracker) set(phase FailurePhase) {
	t.mu.Lock()
	t.phase = phase
	t.mu.Unlock()
}

func (t *failurePhaseTracker) get() FailurePhase {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.phase
}

func (t *failurePhaseTracker) trace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart:             func(httptrace.DNSStartInfo) { t.set(FailurePhaseDNSResolution) },
		ConnectStart:         func(_, _ string) { t.set(FailurePhaseTCPConnect) },
		TLSHandshakeStart:    func() { t.set(FailurePhaseTLSHandshake) },
		WroteRequest:         func(httptrace.WroteRequestInfo) { t.set(FailurePhaseHTTPRequest) },
		GotFirstResponseByte: func() { t.set(FailurePhaseHTTPResponse) },
	}
}

func transportFailurePhase(err error, fallback FailurePhase) FailurePhase {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return FailurePhaseDNSResolution
	}
	if fallback == "" {
		return FailurePhaseHTTPRequest
	}
	return fallback
}

// TLSWithFailurePhase performs a verified TLS handshake while preserving the
// distinction between local resolution, endpoint connection, and handshake
// failures.
func (c NetworkClient) TLSWithFailurePhase(ctx context.Context, host string, port uint16) (TLSEvidence, FailurePhase, error) {
	started := time.Now()
	conn, err := c.dial(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		phase := transportFailurePhase(err, FailurePhaseTCPConnect)
		return TLSEvidence{}, phase, withFailurePhase(phase, err)
	}
	defer conn.Close()
	applyContextDeadline(ctx, conn)
	tlsConn := tls.Client(conn, c.tlsConfig(host))
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return TLSEvidence{}, FailurePhaseTLSHandshake, withFailurePhase(FailurePhaseTLSHandshake, err)
	}
	state := tlsConn.ConnectionState()
	evidence := TLSEvidence{
		Version:     tlsVersionName(state.Version),
		Cipher:      tls.CipherSuiteName(state.CipherSuite),
		HandshakeMS: time.Since(started).Milliseconds(),
	}
	if len(state.PeerCertificates) > 0 {
		evidence.PeerSubject = state.PeerCertificates[0].Subject.CommonName
		evidence.PeerIssuer = state.PeerCertificates[0].Issuer.CommonName
	}
	return evidence, "", nil
}
