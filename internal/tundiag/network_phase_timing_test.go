package tundiag

import (
	"context"
	"crypto/x509"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTLSWithFailurePhaseMeasuresOnlyTLSHandshake(t *testing.T) {
	server := newPhaseTimingTLSServer(t)
	const dialDelay = 200 * time.Millisecond
	client := phaseTimingClient(t, server, dialDelay)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := time.Now()
	evidence, phase, err := client.TLSWithFailurePhase(ctx, "example.com", 443)
	assertTLSHandshakeExcludesDial(t, started, evidence, err)
	if phase != "" {
		t.Fatalf("successful TLS probe returned failure phase %q", phase)
	}
}

func TestNetworkClientTLSMeasuresOnlyTLSHandshake(t *testing.T) {
	server := newPhaseTimingTLSServer(t)
	const dialDelay = 200 * time.Millisecond
	client := phaseTimingClient(t, server, dialDelay)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := time.Now()
	evidence, err := client.TLS(ctx, "example.com", 443)
	assertTLSHandshakeExcludesDial(t, started, evidence, err)
}

func assertTLSHandshakeExcludesDial(t *testing.T, started time.Time, evidence TLSEvidence, err error) {
	t.Helper()
	total := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if excluded := total.Milliseconds() - evidence.HandshakeMS; excluded < 150 {
		t.Fatalf("handshake timing still includes dial delay: total=%s handshake=%dms excluded=%dms", total, evidence.HandshakeMS, excluded)
	}
}

func newPhaseTimingTLSServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestDoHWithFailurePhaseReportsTruthfulHTTPTimings(t *testing.T) {
	const bodyDelay = 120 * time.Millisecond
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query, err := io.ReadAll(io.LimitReader(r.Body, 4097))
		if err != nil || len(query) < 12 {
			http.Error(w, "bad query", http.StatusBadRequest)
			return
		}
		id := binary.BigEndian.Uint16(query[:2])
		response := dnsFixtureResponse(t, id, "example.com", DNSRecordTypeA, DNSRCodeSuccess, net.ParseIP("192.0.2.30").To4())
		w.Header().Set("Content-Type", "application/dns-message")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(bodyDelay)
		_, _ = w.Write(response)
	}))
	defer server.Close()

	client := phaseTimingClient(t, server, 0)
	client.MessageID = func() (uint16, error) { return 0x5151, nil }
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	dnsEvidence, httpEvidence, phase, err := client.DoHWithFailurePhase(
		ctx,
		Target{ID: "local-doh", Kind: TargetDoH, URL: "https://example.com/doh", MaxResponseBytes: 4096},
		"example.com",
		DNSRecordTypeA,
	)
	if err != nil {
		t.Fatal(err)
	}
	if phase != "" {
		t.Fatalf("successful DoH probe returned failure phase %q", phase)
	}
	if !httpEvidence.ResponseAccepted {
		t.Fatalf("valid DoH HTTP response was not marked accepted: %#v", httpEvidence)
	}
	if httpEvidence.BodyMS < 80 {
		t.Fatalf("DoH body timing did not include delayed body read: %#v", httpEvidence)
	}
	if httpEvidence.HeaderMS >= httpEvidence.BodyMS {
		t.Fatalf("DoH header timing includes body transfer: %#v", httpEvidence)
	}
	if len(dnsEvidence.Addresses) != 1 || dnsEvidence.Addresses[0] != "192.0.2.30" {
		t.Fatalf("unexpected DoH DNS evidence: %#v", dnsEvidence)
	}
}

func phaseTimingClient(t *testing.T, server *httptest.Server, dialDelay time.Duration) NetworkClient {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	tlsConfig := server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	tlsConfig.InsecureSkipVerify = false
	tlsConfig.RootCAs = pool
	return NetworkClient{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			if dialDelay > 0 {
				timer := time.NewTimer(dialDelay)
				defer timer.Stop()
				select {
				case <-timer.C:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		},
		TLSConfig: tlsConfig,
	}
}
