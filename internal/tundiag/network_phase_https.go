package tundiag

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"time"
)

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
	tracker := &failurePhaseTracker{phase: FailurePhaseHTTPRequest}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			tracker.set(FailurePhaseTCPConnect)
			conn, err := c.dial(ctx, network, address)
			if err != nil {
				tracker.set(transportFailurePhase(err, FailurePhaseTCPConnect))
			}
			return conn, err
		},
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
