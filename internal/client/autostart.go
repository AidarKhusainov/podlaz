package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

type AutostartClient struct {
	SocketPath string
	Timeout    time.Duration
}

func (c AutostartClient) Enable(ctx context.Context, request api.AutostartConfigureRequest) (api.AutostartStatusResponse, error) {
	if err := api.ValidateAutostartConfigureRequest(request); err != nil {
		return api.AutostartStatusResponse{}, err
	}
	return c.do(ctx, http.MethodPost, api.AutostartConfigurePath, request)
}

func (c AutostartClient) Disable(ctx context.Context) (api.AutostartStatusResponse, error) {
	return c.do(ctx, http.MethodDelete, api.AutostartConfigurePath, nil)
}

func (c AutostartClient) Status(ctx context.Context) (api.AutostartStatusResponse, error) {
	return c.do(ctx, http.MethodGet, api.AutostartStatusPath, nil)
}

func (c AutostartClient) do(ctx context.Context, method, path string, payload any) (api.AutostartStatusResponse, error) {
	socketPath := c.SocketPath
	if socketPath == "" {
		socketPath = api.SocketPath("")
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = defaultLifecycleTimeout
	}

	status, err := retryDaemonSocketReadiness(ctx, func(attemptCtx context.Context) (api.AutostartStatusResponse, error) {
		return c.doViaSocket(attemptCtx, socketPath, timeout, method, path, payload)
	})
	if err == nil {
		return status, nil
	}
	if shouldTryAbstractSocket(socketPath, err) {
		fallback, fallbackErr := c.doViaSocket(ctx, api.AbstractSocketAddress(), timeout, method, path, payload)
		if fallbackErr == nil {
			return fallback, nil
		}
		return api.AutostartStatusResponse{}, abstractSocketFallbackError(err, fallbackErr)
	}
	return api.AutostartStatusResponse{}, err
}

func (c AutostartClient) doViaSocket(ctx context.Context, socketPath string, timeout time.Duration, method, path string, payload any) (api.AutostartStatusResponse, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return api.AutostartStatusResponse{}, err
		}
		body = bytes.NewReader(encoded)
	}

	dialer := net.Dialer{Timeout: timeout}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, "unix", socketPath)
	}}
	defer transport.CloseIdleConnections()

	httpClient := http.Client{Transport: transport, Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, method, "http://podlazd"+path, body)
	if err != nil {
		return api.AutostartStatusResponse{}, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return api.AutostartStatusResponse{}, newDaemonUnavailableError(socketPath, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		message := ""
		if data, readErr := io.ReadAll(resp.Body); readErr == nil {
			message = strings.TrimSpace(string(data))
		}
		if shouldReturnDaemonMessage(resp.StatusCode, message) {
			return api.AutostartStatusResponse{}, errors.New(message)
		}
		return api.AutostartStatusResponse{}, fmt.Errorf("daemon autostart request failed: unexpected HTTP status %s", resp.Status)
	}

	var status api.AutostartStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return api.AutostartStatusResponse{}, fmt.Errorf("daemon autostart response was invalid: %w", err)
	}
	if err := api.ValidateAutostartStatusResponse(status); err != nil {
		return api.AutostartStatusResponse{}, fmt.Errorf("daemon autostart response was invalid: %w", err)
	}
	return status, nil
}
