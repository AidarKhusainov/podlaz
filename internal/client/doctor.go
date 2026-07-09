package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

type DoctorClient struct {
	SocketPath string
	Timeout    time.Duration
}

func (c DoctorClient) Doctor(ctx context.Context) (api.DoctorResponse, error) {
	socketPath := c.SocketPath
	if socketPath == "" {
		socketPath = api.SocketPath("")
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 750 * time.Millisecond
	}

	doctor, err := retryDaemonSocketReadiness(ctx, func(attemptCtx context.Context) (api.DoctorResponse, error) {
		return c.doctorViaSocket(attemptCtx, socketPath, timeout)
	})
	if err == nil {
		return doctor, nil
	}
	if shouldTryAbstractSocket(socketPath, err) {
		fallbackDoctor, fallbackErr := c.doctorViaSocket(ctx, api.AbstractSocketAddress(), timeout)
		if fallbackErr == nil {
			return fallbackDoctor, nil
		}
		return api.DoctorResponse{}, abstractSocketFallbackError(err, fallbackErr)
	}
	return api.DoctorResponse{}, err
}

func (c DoctorClient) doctorViaSocket(ctx context.Context, socketPath string, timeout time.Duration) (api.DoctorResponse, error) {
	dialer := net.Dialer{Timeout: timeout}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}
	defer transport.CloseIdleConnections()

	httpClient := http.Client{Transport: transport, Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://podlazd"+api.DoctorPath, nil)
	if err != nil {
		return api.DoctorResponse{}, err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return api.DoctorResponse{}, newDaemonUnavailableError(socketPath, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return api.DoctorResponse{}, fmt.Errorf("daemon doctor request failed: unexpected HTTP status %s", resp.Status)
	}

	var doctor api.DoctorResponse
	if err := json.NewDecoder(resp.Body).Decode(&doctor); err != nil {
		return api.DoctorResponse{}, fmt.Errorf("daemon doctor response was invalid: %w", err)
	}
	if err := api.ValidateDoctorResponse(doctor); err != nil {
		return api.DoctorResponse{}, fmt.Errorf("daemon doctor response was invalid: %w", err)
	}
	return doctor, nil
}
