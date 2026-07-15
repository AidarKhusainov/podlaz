package tundiag

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"time"
)

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
