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

// HTTPSWithEvidence performs the same bounded HTTPS request as HTTPS while
// recording whether a valid response was accepted and where a later failure
// occurred. PMTU classification relies on these typed phases instead of error
// strings or generic endpoint failures.
func (c NetworkClient) HTTPSWithEvidence(ctx context.Context, target Target) (HTTPEvidence, error) {
	if target.URL == "" {
		return HTTPEvidence{FailurePhase: "request"}, errors.New("HTTPS target URL is empty")
	}
	parsed, err := url.Parse(target.URL)
	if err != nil {
		return HTTPEvidence{FailurePhase: "request"}, fmt.Errorf("parse HTTPS target: %w", err)
	}
	if parsed.Scheme != "https" || parsed.Hostname() == "" {
		return HTTPEvidence{FailurePhase: "request"}, fmt.Errorf("target %s is not an HTTPS URL", target.ID)
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
		return HTTPEvidence{FailurePhase: "request"}, err
	}
	request.Header.Set("User-Agent", "podlaz-diagnostic/1")
	request.Header.Set("Accept", "*/*")
	if target.Kind == TargetPMTU && strings.Contains(strings.ToLower(target.Method), "range") {
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
		phase := "request"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || diagnosticTimeoutError(err) {
			phase = "request_timeout"
		} else if errors.Is(ctx.Err(), context.Canceled) {
			phase = "request_cancelled"
		}
		return HTTPEvidence{FailurePhase: phase}, err
	}
	defer response.Body.Close()
	mu.Lock()
	headerAt := firstByte
	mu.Unlock()
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
			return evidence, fmt.Errorf("unexpected HTTP redirect %d without Location", response.StatusCode)
		}
		redirectURL, parseErr := url.Parse(location)
		if parseErr != nil {
			return evidence, fmt.Errorf("invalid HTTP redirect location %q: %w", location, parseErr)
		}
		if redirectURL.Scheme != "https" {
			return evidence, fmt.Errorf("refused HTTP downgrade redirect to %s", location)
		}
		return evidence, fmt.Errorf("unexpected HTTPS redirect to %s", location)
	}
	if !targetAcceptsStatus(target, response.StatusCode) {
		evidence.FailurePhase = "status"
		return evidence, fmt.Errorf("unexpected HTTP status %d; expected %s", response.StatusCode, target.ExpectedSuccess)
	}
	evidence.ResponseAccepted = true
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
		return evidence, readErr
	}
	return evidence, nil
}

func diagnosticTimeoutError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func isTransportReadError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr)
}
