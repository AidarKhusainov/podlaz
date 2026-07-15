package tundiag

import (
	"bytes"
	"context"
	"crypto/tls"
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
		DNSStart:              func(httptrace.DNSStartInfo) { t.set(FailurePhaseDNSResolution) },
		ConnectStart:          func(_, _ string) { t.set(FailurePhaseTCPConnect) },
		TLSHandshakeStart:     func() { t.set(FailurePhaseTLSHandshake) },
		WroteRequest:          func(httptrace.WroteRequestInfo) { t.set(FailurePhaseHTTPRequest) },
		GotFirstResponseByte:  func() { t.set(FailurePhaseHTTPResponse) },
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

// HTTPSWithFailurePhase performs the bounded HTTPS request and reports the
// transport phase independently from the classification assigned by Runner.
func (c NetworkClient) HTTPSWithFailurePhase(ctx context.Context, target Target) (HTTPEvidence, FailurePhase, error) {
	if target.URL == "" {
		err := errors.New("HTTPS target URL is empty")
		return HTTPEvidence{FailurePhase: "request"}, FailurePhaseHTTPRequest, withFailurePhase(FailurePhaseHTTPRequest, err)
	}
	parsed, err := url.Parse(target.URL)
	if err != nil {
		return HTTPEvidence{FailurePhase: "request"}, FailurePhaseHTTPRequest, withFailurePhase(FailurePhaseHTTPRequest, fmt.Errorf("parse HTTPS target: %w", err))
	}
	if parsed.Scheme != "https" || parsed.Hostname() == "" {
		err := fmt.Errorf("target %s is not an HTTPS URL", target.ID)
		return HTTPEvidence{FailurePhase: "request"}, FailurePhaseHTTPRequest, withFailurePhase(FailurePhaseHTTPRequest, err)
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
		Transport:     transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL, nil)
	if err != nil {
		return HTTPEvidence{FailurePhase: "request"}, FailurePhaseHTTPRequest, withFailurePhase(FailurePhaseHTTPRequest, err)
	}
	request.Header.Set("User-Agent", "podlaz-diagnostic/1")
	request.Header.Set("Accept", "*/*")
	if target.Kind == TargetPMTU && strings.Contains(strings.ToLower(target.Method), "range") {
		request.Header.Set("Range", fmt.Sprintf("bytes=0-%d", maxBytes-1))
	}

	started := time.Now()
	var firstByte time.Time
	var firstByteMu sync.Mutex
	tracker := &failurePhaseTracker{phase: FailurePhaseHTTPRequest}
	trace := tracker.trace()
	trace.GotFirstResponseByte = func() {
		tracker.set(FailurePhaseHTTPResponse)
		firstByteMu.Lock()
		firstByte = time.Now()
		firstByteMu.Unlock()
	}
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), trace))
	response, err := client.Do(request)
	if err != nil {
		phase := transportFailurePhase(err, tracker.get())
		evidencePhase := "request"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || diagnosticTimeoutError(err) {
			evidencePhase = "request_timeout"
		} else if errors.Is(ctx.Err(), context.Canceled) {
			evidencePhase = "request_cancelled"
		}
		return HTTPEvidence{FailurePhase: evidencePhase}, phase, withFailurePhase(phase, err)
	}
	defer response.Body.Close()
	tracker.set(FailurePhaseHTTPResponse)
	firstByteMu.Lock()
	headerAt := firstByte
	firstByteMu.Unlock()
	if headerAt.IsZero() {
		headerAt = time.Now()
	}
	evidence := HTTPEvidence{
		StatusCode:    response.StatusCode,
		Location:      response.Header.Get("Location"),
		ContentLength: response.ContentLength,
		HeaderMS:      headerAt.Sub(started).Milliseconds(),
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		evidence.FailurePhase = "redirect"
		location := strings.TrimSpace(response.Header.Get("Location"))
		if location == "" {
			err := fmt.Errorf("unexpected HTTP redirect %d without Location", response.StatusCode)
			return evidence, FailurePhaseHTTPResponse, withFailurePhase(FailurePhaseHTTPResponse, err)
		}
		redirectURL, parseErr := url.Parse(location)
		if parseErr != nil {
			err := fmt.Errorf("invalid HTTP redirect location %q: %w", location, parseErr)
			return evidence, FailurePhaseHTTPResponse, withFailurePhase(FailurePhaseHTTPResponse, err)
		}
		if redirectURL.Scheme != "https" {
			err := fmt.Errorf("refused HTTP downgrade redirect to %s", location)
			return evidence, FailurePhaseHTTPResponse, withFailurePhase(FailurePhaseHTTPResponse, err)
		}
		err := fmt.Errorf("unexpected HTTPS redirect to %s", location)
		return evidence, FailurePhaseHTTPResponse, withFailurePhase(FailurePhaseHTTPResponse, err)
	}
	if !targetAcceptsStatus(target, response.StatusCode) {
		evidence.FailurePhase = "status"
		err := fmt.Errorf("unexpected HTTP status %d; expected %s", response.StatusCode, target.ExpectedSuccess)
		return evidence, FailurePhaseHTTPResponse, withFailurePhase(FailurePhaseHTTPResponse, err)
	}
	evidence.ResponseAccepted = true
	tracker.set(FailurePhaseHTTPBody)
	bodyStarted := time.Now()
	count, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, maxBytes))
	evidence.BytesRead = count
	evidence.BodyMS = time.Since(bodyStarted).Milliseconds()
	if readErr != nil {
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded) || diagnosticTimeoutError(readErr):
			evidence.FailurePhase = "body_timeout"
		case errors.Is(ctx.Err(), context.Canceled):
			evidence.FailurePhase = "body_cancelled"
		case isTransportReadError(readErr):
			evidence.FailurePhase = "body_transport"
		default:
			evidence.FailurePhase = "body_error"
		}
		return evidence, FailurePhaseHTTPBody, withFailurePhase(FailurePhaseHTTPBody, readErr)
	}
	return evidence, "", nil
}

// DoHWithFailurePhase performs a bounded RFC 8484 request and preserves the
// phase that failed before parsing the DNS payload.
func (c NetworkClient) DoHWithFailurePhase(ctx context.Context, target Target, name string, recordType uint16) (DNSEvidence, HTTPEvidence, FailurePhase, error) {
	id, query, err := c.dnsQuery(name, recordType)
	if err != nil {
		return DNSEvidence{}, HTTPEvidence{}, FailurePhaseHTTPRequest, withFailurePhase(FailurePhaseHTTPRequest, err)
	}
	parsed, err := url.Parse(target.URL)
	if err != nil {
		err = fmt.Errorf("parse DoH target: %w", err)
		return DNSEvidence{}, HTTPEvidence{}, FailurePhaseHTTPRequest, withFailurePhase(FailurePhaseHTTPRequest, err)
	}
	if parsed.Scheme != "https" || parsed.Hostname() == "" {
		err := fmt.Errorf("target %s is not an HTTPS DoH URL", target.ID)
		return DNSEvidence{}, HTTPEvidence{}, FailurePhaseHTTPRequest, withFailurePhase(FailurePhaseHTTPRequest, err)
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
		Transport:     transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL, bytes.NewReader(query))
	if err != nil {
		return DNSEvidence{}, HTTPEvidence{}, FailurePhaseHTTPRequest, withFailurePhase(FailurePhaseHTTPRequest, err)
	}
	request.Header.Set("Content-Type", "application/dns-message")
	request.Header.Set("Accept", "application/dns-message")
	request.Header.Set("User-Agent", "podlaz-diagnostic/1")
	tracker := &failurePhaseTracker{phase: FailurePhaseHTTPRequest}
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), tracker.trace()))
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		phase := transportFailurePhase(err, tracker.get())
		return DNSEvidence{}, HTTPEvidence{}, phase, withFailurePhase(phase, err)
	}
	defer response.Body.Close()
	tracker.set(FailurePhaseHTTPBody)
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
		return DNSEvidence{}, httpEvidence, FailurePhaseHTTPBody, withFailurePhase(FailurePhaseHTTPBody, err)
	}
	tracker.set(FailurePhaseHTTPResponse)
	if int64(len(body)) > maxBytes {
		err := fmt.Errorf("DoH response exceeds %d bytes", maxBytes)
		return DNSEvidence{}, httpEvidence, FailurePhaseHTTPResponse, withFailurePhase(FailurePhaseHTTPResponse, err)
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		err := fmt.Errorf("unexpected DoH redirect to %q", response.Header.Get("Location"))
		return DNSEvidence{}, httpEvidence, FailurePhaseHTTPResponse, withFailurePhase(FailurePhaseHTTPResponse, err)
	}
	if !targetAcceptsStatus(target, response.StatusCode) {
		err := fmt.Errorf("unexpected DoH HTTP status %d; expected %s", response.StatusCode, target.ExpectedSuccess)
		return DNSEvidence{}, httpEvidence, FailurePhaseHTTPResponse, withFailurePhase(FailurePhaseHTTPResponse, err)
	}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if !strings.HasPrefix(contentType, "application/dns-message") {
		err := fmt.Errorf("DoH content type %q is not application/dns-message", contentType)
		return DNSEvidence{}, httpEvidence, FailurePhaseHTTPResponse, withFailurePhase(FailurePhaseHTTPResponse, err)
	}
	dnsEvidence, err := ParseDNSResponse(body, id, name, recordType)
	if err != nil {
		return DNSEvidence{}, httpEvidence, FailurePhaseHTTPResponse, withFailurePhase(FailurePhaseHTTPResponse, err)
	}
	dnsEvidence.Server = target.URL
	return dnsEvidence, httpEvidence, "", nil
}
