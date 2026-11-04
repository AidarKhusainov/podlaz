package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

// Full-TUN connectivity is verified twice (including two bounded DNS probes
// per pass). A failed replacement can additionally require exact rollback and
// restoring a previous protected generation. Keep the client deadline beyond
// those daemon-owned bounded phases rather than timing out during verification.
const defaultLifecycleTimeout = 4 * time.Minute

// ErrLifecycleOutcomeUnknown means the daemon socket connected, but the client
// cannot confirm whether the requested operation committed.
var ErrLifecycleOutcomeUnknown = errors.New("daemon lifecycle outcome unknown")

type LifecycleClient struct {
	SocketPath string
	Timeout    time.Duration
}

func (c LifecycleClient) Connect(ctx context.Context, req api.ConnectRequest) (api.LifecycleResponse, error) {
	return c.do(ctx, "connect", api.ConnectPath, req)
}

func (c LifecycleClient) Disconnect(ctx context.Context) (api.LifecycleResponse, error) {
	return c.do(ctx, "disconnect", api.DisconnectPath, nil)
}

func (c LifecycleClient) do(ctx context.Context, operation, path string, payload any) (api.LifecycleResponse, error) {
	socketPath := c.SocketPath
	if socketPath == "" {
		socketPath = api.SocketPath("")
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = defaultLifecycleTimeout
	}

	lifecycle, err := retryDaemonSocketReadiness(ctx, func(attemptCtx context.Context) (api.LifecycleResponse, error) {
		return c.doViaSocket(attemptCtx, socketPath, timeout, operation, path, payload)
	})
	if err == nil {
		return lifecycle, nil
	}
	if shouldTryAbstractSocket(socketPath, err) {
		fallbackLifecycle, fallbackErr := c.doViaSocket(ctx, api.AbstractSocketAddress(), timeout, operation, path, payload)
		if fallbackErr == nil {
			return fallbackLifecycle, nil
		}
		return api.LifecycleResponse{}, abstractSocketFallbackError(err, fallbackErr)
	}
	return api.LifecycleResponse{}, err
}

func (c LifecycleClient) doViaSocket(ctx context.Context, socketPath string, timeout time.Duration, operation, path string, payload any) (api.LifecycleResponse, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return api.LifecycleResponse{}, err
		}
		body = bytes.NewReader(encoded)
	}

	dial := newDaemonDialTracker(socketPath, timeout)
	transport := &http.Transport{DialContext: dial.DialContext}
	defer transport.CloseIdleConnections()

	httpClient := http.Client{Transport: transport, Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://podlazd"+path, body)
	if err != nil {
		return api.LifecycleResponse{}, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		if dial.connected.Load() {
			return api.LifecycleResponse{}, unknownLifecycleOutcome(operation, err)
		}
		return api.LifecycleResponse{}, dial.requestError(operation, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		message := ""
		if data, readErr := io.ReadAll(resp.Body); readErr == nil {
			message = strings.TrimSpace(string(data))
		}
		if shouldReturnDaemonMessage(resp.StatusCode, message) {
			return api.LifecycleResponse{}, errors.New(message)
		}
		return api.LifecycleResponse{}, api.LifecycleHTTPError(operation, resp.Status, message)
	}

	var lifecycle api.LifecycleResponse
	if err := json.NewDecoder(resp.Body).Decode(&lifecycle); err != nil {
		return api.LifecycleResponse{}, unknownLifecycleOutcome(operation, fmt.Errorf("invalid daemon response: %w", err))
	}
	if err := api.ValidateLifecycleResponse(lifecycle); err != nil {
		return api.LifecycleResponse{}, unknownLifecycleOutcome(operation, fmt.Errorf("invalid daemon response: %w", err))
	}
	return lifecycle, nil
}

// A post-dial failure must never trigger an automatic second mutation.
func unknownLifecycleOutcome(operation string, cause error) error {
	return fmt.Errorf("%w: daemon %s response could not be confirmed; the operation may have committed. Check `podlaz status` and `podlaz debug recover` before retrying: %v", ErrLifecycleOutcomeUnknown, operation, cause)
}

func shouldReturnDaemonMessage(statusCode int, message string) bool {
	if strings.TrimSpace(message) == "" {
		return false
	}
	return statusCode == http.StatusBadRequest ||
		statusCode == http.StatusForbidden ||
		statusCode == http.StatusConflict ||
		statusCode == http.StatusServiceUnavailable
}
