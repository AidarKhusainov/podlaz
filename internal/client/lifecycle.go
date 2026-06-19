package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

const defaultLifecycleTimeout = 30 * time.Second

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

	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return api.LifecycleResponse{}, err
		}
		body = bytes.NewReader(encoded)
	}

	dialer := net.Dialer{Timeout: timeout}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}
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
		return api.LifecycleResponse{}, fmt.Errorf("%w: %s", ErrDaemonUnavailable, unavailableDetail(socketPath, err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		message := ""
		if data, readErr := io.ReadAll(resp.Body); readErr == nil {
			message = strings.TrimSpace(string(data))
		}
		return api.LifecycleResponse{}, api.LifecycleHTTPError(operation, resp.Status, message)
	}

	var lifecycle api.LifecycleResponse
	if err := json.NewDecoder(resp.Body).Decode(&lifecycle); err != nil {
		return api.LifecycleResponse{}, fmt.Errorf("daemon %s response was invalid: %w", operation, err)
	}
	if err := api.ValidateLifecycleResponse(lifecycle); err != nil {
		return api.LifecycleResponse{}, fmt.Errorf("daemon %s response was invalid: %w", operation, err)
	}
	return lifecycle, nil
}
