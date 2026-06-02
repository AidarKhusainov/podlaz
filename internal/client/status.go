package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/AidarKhusainov/tunwarden/internal/api"
)

var ErrDaemonUnavailable = errors.New("tunwardend unavailable")

type StatusClient struct {
	SocketPath string
	Timeout    time.Duration
}

func (c StatusClient) Status(ctx context.Context) (api.StatusResponse, error) {
	socketPath := c.SocketPath
	if socketPath == "" {
		socketPath = api.SocketPath("")
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 750 * time.Millisecond
	}

	dialer := net.Dialer{Timeout: timeout}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}
	defer transport.CloseIdleConnections()

	httpClient := http.Client{Transport: transport, Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://tunwardend"+api.StatusPath, nil)
	if err != nil {
		return api.StatusResponse{}, err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return api.StatusResponse{}, fmt.Errorf("%w: %s", ErrDaemonUnavailable, unavailableDetail(socketPath, err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return api.StatusResponse{}, fmt.Errorf("daemon status request failed: unexpected HTTP status %s", resp.Status)
	}

	var status api.StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return api.StatusResponse{}, fmt.Errorf("daemon status response was invalid: %w", err)
	}
	if err := api.ValidateStatusResponse(status); err != nil {
		return api.StatusResponse{}, fmt.Errorf("daemon status response was invalid: %w", err)
	}
	return status, nil
}

func IsDaemonUnavailable(err error) bool { return errors.Is(err, ErrDaemonUnavailable) }

func UnavailableMessage(err error) string {
	if err == nil {
		return "daemon is not reachable; start tunwardend"
	}
	message := stringsAfterWrapped(err.Error())
	if message == ErrDaemonUnavailable.Error() {
		return "daemon is not reachable; start tunwardend"
	}
	return message
}

func unavailableDetail(socketPath string, err error) string {
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Sprintf("daemon socket %s does not exist; start tunwardend", socketPath)
	}
	if isTimeout(err) {
		return fmt.Sprintf("daemon socket %s did not respond before timeout; start or restart tunwardend", socketPath)
	}
	return fmt.Sprintf("daemon socket %s is not reachable; start or restart tunwardend", socketPath)
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func stringsAfterWrapped(s string) string {
	const prefix = "tunwardend unavailable: "
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):]
	}
	return s
}
