package tundiag

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type DialContextFunc func(context.Context, string, string) (net.Conn, error)

type NetworkClient struct {
	DialContext DialContextFunc
	Resolver    *net.Resolver
	MessageID   func() (uint16, error)
	TLSConfig   *tls.Config
}

func (c NetworkClient) DNSUDP(ctx context.Context, server, name string, recordType uint16) (DNSEvidence, error) {
	id, query, err := c.dnsQuery(name, recordType)
	if err != nil {
		return DNSEvidence{}, err
	}
	address := dnsServerAddress(server)
	conn, err := c.dial(ctx, "udp", address)
	if err != nil {
		return DNSEvidence{}, fmt.Errorf("dial DNS UDP server %s: %w", address, err)
	}
	defer conn.Close()
	applyContextDeadline(ctx, conn)
	if _, err := conn.Write(query); err != nil {
		return DNSEvidence{}, fmt.Errorf("write DNS UDP query: %w", err)
	}
	buffer := make([]byte, 65535)
	count, err := conn.Read(buffer)
	if err != nil {
		return DNSEvidence{}, fmt.Errorf("read DNS UDP response: %w", err)
	}
	evidence, err := ParseDNSResponse(buffer[:count], id, name, recordType)
	if err != nil {
		return DNSEvidence{}, err
	}
	evidence.Server = address
	return evidence, nil
}

func (c NetworkClient) DNSTCP(ctx context.Context, server, name string, recordType uint16) (DNSEvidence, error) {
	id, query, err := c.dnsQuery(name, recordType)
	if err != nil {
		return DNSEvidence{}, err
	}
	address := dnsServerAddress(server)
	conn, err := c.dial(ctx, "tcp", address)
	if err != nil {
		return DNSEvidence{}, fmt.Errorf("dial DNS TCP server %s: %w", address, err)
	}
	defer conn.Close()
	applyContextDeadline(ctx, conn)
	if len(query) > 65535 {
		return DNSEvidence{}, errors.New("DNS TCP query exceeds 65535 bytes")
	}
	frame := make([]byte, 2+len(query))
	binary.BigEndian.PutUint16(frame[:2], uint16(len(query)))
	copy(frame[2:], query)
	if _, err := conn.Write(frame); err != nil {
		return DNSEvidence{}, fmt.Errorf("write DNS TCP query: %w", err)
	}
	var lengthPrefix [2]byte
	if _, err := io.ReadFull(conn, lengthPrefix[:]); err != nil {
		return DNSEvidence{}, fmt.Errorf("read DNS TCP response length: %w", err)
	}
	length := int(binary.BigEndian.Uint16(lengthPrefix[:]))
	if length < 12 {
		return DNSEvidence{}, fmt.Errorf("DNS TCP response length %d is too short", length)
	}
	message := make([]byte, length)
	if _, err := io.ReadFull(conn, message); err != nil {
		return DNSEvidence{}, fmt.Errorf("read DNS TCP response: %w", err)
	}
	evidence, err := ParseDNSResponse(message, id, name, recordType)
	if err != nil {
		return DNSEvidence{}, err
	}
	evidence.Server = address
	return evidence, nil
}

func (c NetworkClient) Resolve(ctx context.Context, name string) ([]string, error) {
	resolver := c.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addresses, err := resolver.LookupIPAddr(ctx, name)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(addresses))
	seen := make(map[string]struct{}, len(addresses))
	for _, address := range addresses {
		value := address.IP.String()
		if value == "<nil>" || value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("resolver returned no addresses for %s", name)
	}
	return out, nil
}

func (c NetworkClient) TCP(ctx context.Context, host string, port uint16) (time.Duration, error) {
	started := time.Now()
	conn, err := c.dial(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	return time.Since(started), nil
}

func (c NetworkClient) TLS(ctx context.Context, host string, port uint16) (TLSEvidence, error) {
	started := time.Now()
	conn, err := c.dial(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		return TLSEvidence{}, err
	}
	defer conn.Close()
	applyContextDeadline(ctx, conn)
	tlsConn := tls.Client(conn, c.tlsConfig(host))
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return TLSEvidence{}, err
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
	return evidence, nil
}

func (c NetworkClient) HTTPS(ctx context.Context, target Target) (HTTPEvidence, error) {
	if target.URL == "" {
		return HTTPEvidence{}, errors.New("HTTPS target URL is empty")
	}
	parsed, err := url.Parse(target.URL)
	if err != nil {
		return HTTPEvidence{}, fmt.Errorf("parse HTTPS target: %w", err)
	}
	if parsed.Scheme != "https" || parsed.Hostname() == "" {
		return HTTPEvidence{}, fmt.Errorf("target %s is not an HTTPS URL", target.ID)
	}
	maxBytes := target.MaxResponseBytes
	if maxBytes <= 0 {
		maxBytes = 4096
	}

	transport := &http.Transport{
		Proxy:             nil,
		DialContext:       c.dial,
		DisableKeepAlives: true,
		TLSClientConfig:   c.tlsConfig(parsed.Hostname()),
		ForceAttemptHTTP2: true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL, nil)
	if err != nil {
		return HTTPEvidence{}, err
	}
	request.Header.Set("User-Agent", "podlaz-diagnostic/1")
	request.Header.Set("Accept", "*/*")
	if target.Kind == TargetPMTU {
		request.Header.Set("Range", fmt.Sprintf("bytes=0-%d", maxBytes-1))
	}

	started := time.Now()
	var firstByte time.Time
	var mu sync.Mutex
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), &httptrace.ClientTrace{
		GotFirstResponseByte: func() {
			mu.Lock()
			firstByte = time.Now()
			mu.Unlock()
		},
	}))
	response, err := client.Do(request)
	if err != nil {
		return HTTPEvidence{}, err
	}
	defer response.Body.Close()
	mu.Lock()
	headerAt := firstByte
	mu.Unlock()
	if headerAt.IsZero() {
		headerAt = time.Now()
	}
	bodyStarted := time.Now()
	count, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, maxBytes))
	evidence := HTTPEvidence{
		StatusCode:    response.StatusCode,
		Location:      response.Header.Get("Location"),
		ContentLength: response.ContentLength,
		BytesRead:     count,
		HeaderMS:      headerAt.Sub(started).Milliseconds(),
		BodyMS:        time.Since(bodyStarted).Milliseconds(),
	}
	if readErr != nil {
		return evidence, readErr
	}
	if response.StatusCode >= 500 {
		return evidence, fmt.Errorf("HTTP status %d", response.StatusCode)
	}
	return evidence, nil
}

func (c NetworkClient) DoH(ctx context.Context, target Target, name string, recordType uint16) (DNSEvidence, HTTPEvidence, error) {
	id, query, err := c.dnsQuery(name, recordType)
	if err != nil {
		return DNSEvidence{}, HTTPEvidence{}, err
	}
	parsed, err := url.Parse(target.URL)
	if err != nil {
		return DNSEvidence{}, HTTPEvidence{}, fmt.Errorf("parse DoH target: %w", err)
	}
	if parsed.Scheme != "https" || parsed.Hostname() == "" {
		return DNSEvidence{}, HTTPEvidence{}, fmt.Errorf("target %s is not an HTTPS DoH URL", target.ID)
	}
	transport := &http.Transport{
		Proxy:             nil,
		DialContext:       c.dial,
		DisableKeepAlives: true,
		TLSClientConfig:   c.tlsConfig(parsed.Hostname()),
		ForceAttemptHTTP2: true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL, bytes.NewReader(query))
	if err != nil {
		return DNSEvidence{}, HTTPEvidence{}, err
	}
	request.Header.Set("Content-Type", "application/dns-message")
	request.Header.Set("Accept", "application/dns-message")
	request.Header.Set("User-Agent", "podlaz-diagnostic/1")
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return DNSEvidence{}, HTTPEvidence{}, err
	}
	defer response.Body.Close()
	maxBytes := target.MaxResponseBytes
	if maxBytes <= 0 {
		maxBytes = 4096
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	httpEvidence := HTTPEvidence{
		StatusCode:    response.StatusCode,
		Location:      response.Header.Get("Location"),
		ContentLength: response.ContentLength,
		BytesRead:     int64(len(body)),
		HeaderMS:      time.Since(started).Milliseconds(),
	}
	if err != nil {
		return DNSEvidence{}, httpEvidence, err
	}
	if int64(len(body)) > maxBytes {
		return DNSEvidence{}, httpEvidence, fmt.Errorf("DoH response exceeds %d bytes", maxBytes)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return DNSEvidence{}, httpEvidence, fmt.Errorf("DoH HTTP status %d", response.StatusCode)
	}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if !strings.HasPrefix(contentType, "application/dns-message") {
		return DNSEvidence{}, httpEvidence, fmt.Errorf("DoH content type %q is not application/dns-message", contentType)
	}
	dnsEvidence, err := ParseDNSResponse(body, id, name, recordType)
	if err != nil {
		return DNSEvidence{}, httpEvidence, err
	}
	dnsEvidence.Server = target.URL
	return dnsEvidence, httpEvidence, nil
}

func (c NetworkClient) IsNXDomain(err error) bool {
	var dnsError *net.DNSError
	return errors.As(err, &dnsError) && dnsError.IsNotFound
}

func (c NetworkClient) dnsQuery(name string, recordType uint16) (uint16, []byte, error) {
	idFunc := c.MessageID
	if idFunc == nil {
		idFunc = randomDNSMessageID
	}
	id, err := idFunc()
	if err != nil {
		return 0, nil, fmt.Errorf("generate DNS message id: %w", err)
	}
	query, err := BuildDNSQuery(id, name, recordType)
	return id, query, err
}

func (c NetworkClient) dial(ctx context.Context, network, address string) (net.Conn, error) {
	if c.DialContext != nil {
		return c.DialContext(ctx, network, address)
	}
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

func (c NetworkClient) tlsConfig(host string) *tls.Config {
	var config *tls.Config
	if c.TLSConfig != nil {
		config = c.TLSConfig.Clone()
	} else {
		config = &tls.Config{}
	}
	if config.MinVersion == 0 {
		config.MinVersion = tls.VersionTLS12
	}
	config.ServerName = host
	return config
}

func randomDNSMessageID() (uint16, error) {
	var value [2]byte
	if _, err := rand.Read(value[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(value[:]), nil
}

func dnsServerAddress(server string) string {
	server = strings.TrimSpace(server)
	if server == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(server); err == nil {
		return server
	}
	return net.JoinHostPort(server, "53")
}

func applyContextDeadline(ctx context.Context, conn net.Conn) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
}

func tlsVersionName(version uint16) string {
	switch version {
	case tls.VersionTLS10:
		return "TLS 1.0"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	default:
		return fmt.Sprintf("0x%x", version)
	}
}
