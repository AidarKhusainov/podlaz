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

func TestNetworkClientDNSUDPAndTCPUseBoundedWireProtocol(t *testing.T) {
	for _, network := range []string{"udp", "tcp"} {
		t.Run(network, func(t *testing.T) {
			clientConn, serverConn := net.Pipe()
			defer clientConn.Close()
			defer serverConn.Close()
			go serveDNSFixture(t, serverConn, network)
			client := NetworkClient{
				DialContext: func(context.Context, string, string) (net.Conn, error) { return clientConn, nil },
				MessageID:   func() (uint16, error) { return 0x4242, nil },
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var evidence DNSEvidence
			var err error
			if network == "udp" { evidence, err = client.DNSUDP(ctx, "192.0.2.53", "example.com", DNSRecordTypeA) } else { evidence, err = client.DNSTCP(ctx, "192.0.2.53", "example.com", DNSRecordTypeA) }
			if err != nil { t.Fatal(err) }
			if len(evidence.Addresses) != 1 || evidence.Addresses[0] != "192.0.2.20" { t.Fatalf("unexpected evidence: %#v", evidence) }
		})
	}
}

func TestNetworkClientHTTPSAndDoHUseLocalTLSFixtures(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/small":
			w.WriteHeader(http.StatusNoContent)
		case "/doh":
			query, err := io.ReadAll(io.LimitReader(r.Body, 4097))
			if err != nil || len(query) < 12 { http.Error(w, "bad query", http.StatusBadRequest); return }
			id := binary.BigEndian.Uint16(query[:2])
			response := dnsFixtureResponse(t, id, "example.com", DNSRecordTypeA, DNSRCodeSuccess, net.ParseIP("192.0.2.30").To4())
			w.Header().Set("Content-Type", "application/dns-message")
			_, _ = w.Write(response)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	client := NetworkClient{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) { return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String()) },
		TLSConfig:   server.Client().Transport.(*http.Transport).TLSClientConfig.Clone(),
		MessageID:   func() (uint16, error) { return 0x5151, nil },
	}
	client.TLSConfig.RootCAs = pool
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	httpEvidence, err := client.HTTPS(ctx, Target{ID: "local-small", Kind: TargetHTTPS, URL: "https://example.com/small", MaxResponseBytes: 1024})
	if err != nil || httpEvidence.StatusCode != http.StatusNoContent { t.Fatalf("unexpected HTTPS result: %#v err=%v", httpEvidence, err) }
	dnsEvidence, dohHTTP, err := client.DoH(ctx, Target{ID: "local-doh", Kind: TargetDoH, URL: "https://example.com/doh", MaxResponseBytes: 4096}, "example.com", DNSRecordTypeA)
	if err != nil { t.Fatal(err) }
	if dohHTTP.StatusCode != http.StatusOK || len(dnsEvidence.Addresses) != 1 || dnsEvidence.Addresses[0] != "192.0.2.30" { t.Fatalf("unexpected DoH result: dns=%#v http=%#v", dnsEvidence, dohHTTP) }
}

func serveDNSFixture(t *testing.T, conn net.Conn, network string) {
	t.Helper()
	var query []byte
	if network == "tcp" {
		var prefix [2]byte
		if _, err := io.ReadFull(conn, prefix[:]); err != nil { return }
		query = make([]byte, int(binary.BigEndian.Uint16(prefix[:])))
		if _, err := io.ReadFull(conn, query); err != nil { return }
	} else {
		buffer := make([]byte, 4096)
		count, err := conn.Read(buffer)
		if err != nil { return }
		query = buffer[:count]
	}
	id := binary.BigEndian.Uint16(query[:2])
	response := dnsFixtureResponse(t, id, "example.com", DNSRecordTypeA, DNSRCodeSuccess, net.ParseIP("192.0.2.20").To4())
	if network == "tcp" {
		frame := make([]byte, 2+len(response))
		binary.BigEndian.PutUint16(frame[:2], uint16(len(response)))
		copy(frame[2:], response)
		_, _ = conn.Write(frame)
		return
	}
	_, _ = conn.Write(response)
}
