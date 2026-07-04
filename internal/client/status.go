package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

var ErrDaemonUnavailable = errors.New("podlazd unavailable")
var ErrDaemonPermissionDenied = errors.New("daemon socket permission denied")

const (
	defaultStatusTimeout       = 750 * time.Millisecond
	daemonReadinessRetryWindow = 2 * time.Second
	daemonReadinessRetryDelay  = 100 * time.Millisecond
)

type daemonUnavailableError struct {
	detail           string
	cause            error
	permissionDenied bool
}

func (e daemonUnavailableError) Error() string {
	return ErrDaemonUnavailable.Error() + ": " + e.detail
}

func (e daemonUnavailableError) Unwrap() error {
	return e.cause
}

func (e daemonUnavailableError) Is(target error) bool {
	switch target {
	case ErrDaemonUnavailable:
		return true
	case ErrDaemonPermissionDenied:
		return e.permissionDenied
	default:
		return false
	}
}

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
		timeout = defaultStatusTimeout
	}

	status, err := retryDaemonSocketReadiness(ctx, func(attemptCtx context.Context) (api.StatusResponse, error) {
		return c.statusViaSocket(attemptCtx, socketPath, timeout)
	})
	if err == nil {
		return status, nil
	}
	if shouldTryAbstractSocket(socketPath, err) {
		fallbackStatus, fallbackErr := c.statusViaSocket(ctx, api.AbstractSocketAddress(), timeout)
		if fallbackErr == nil {
			return fallbackStatus, nil
		}
		return api.StatusResponse{}, fallbackErr
	}
	return api.StatusResponse{}, err
}

func (c StatusClient) statusViaSocket(ctx context.Context, socketPath string, timeout time.Duration) (api.StatusResponse, error) {
	dialer := net.Dialer{Timeout: timeout}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}
	defer transport.CloseIdleConnections()

	httpClient := http.Client{Transport: transport, Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://podlazd"+api.StatusPath, nil)
	if err != nil {
		return api.StatusResponse{}, err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return api.StatusResponse{}, daemonUnavailableError{
			detail:           unavailableDetail(socketPath, err),
			cause:            err,
			permissionDenied: isPermissionDenied(err),
		}
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

func retryDaemonSocketReadiness[T any](ctx context.Context, fn func(context.Context) (T, error)) (T, error) {
	deadline := time.Now().Add(daemonReadinessRetryWindow)
	var zero T
	var lastErr error
	for {
		result, err := fn(ctx)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !shouldRetryDaemonReadiness(err) || time.Now().After(deadline) {
			return zero, lastErr
		}
		timer := time.NewTimer(daemonReadinessRetryDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
}

func shouldRetryDaemonReadiness(err error) bool {
	var unavailable daemonUnavailableError
	if !errors.As(err, &unavailable) {
		return false
	}
	return errors.Is(unavailable.cause, os.ErrNotExist) || errors.Is(unavailable.cause, syscall.ECONNREFUSED)
}

func IsDaemonUnavailable(err error) bool { return errors.Is(err, ErrDaemonUnavailable) }

func IsDaemonPermissionDenied(err error) bool { return errors.Is(err, ErrDaemonPermissionDenied) }

func UnavailableMessage(err error) string {
	if err == nil {
		return "daemon is not reachable; start podlazd"
	}
	var unavailable daemonUnavailableError
	if errors.As(err, &unavailable) && unavailable.detail != "" {
		return unavailable.detail
	}
	message := stringsAfterWrapped(err.Error())
	if message == ErrDaemonUnavailable.Error() {
		return "daemon is not reachable; start podlazd"
	}
	return message
}

func unavailableDetail(socketPath string, err error) string {
	if isPermissionDenied(err) {
		return fmt.Sprintf("daemon socket %s is not accessible (permission denied); packaged installs retry a polkit-gated abstract socket when podlazd.service runs with PODLAZ_POLKIT_AUTHORIZATION=required, or an administrator may use the explicit podlaz group fallback", socketPath)
	}
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Sprintf("daemon socket %s does not exist; start podlazd", socketPath)
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return fmt.Sprintf("daemon socket %s refused the connection; remove a stale socket or restart podlazd", socketPath)
	}
	if isTimeout(err) {
		return fmt.Sprintf("daemon socket %s did not respond before timeout; start or restart podlazd", socketPath)
	}
	return fmt.Sprintf("daemon socket %s is not reachable; start or restart podlazd", socketPath)
}

func shouldTryAbstractSocket(socketPath string, err error) bool {
	return socketPath == api.SocketPath("") && isPermissionDenied(err)
}

func isPermissionDenied(err error) bool {
	return errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM)
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func stringsAfterWrapped(s string) string {
	const prefix = "podlazd unavailable: "
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):]
	}
	return s
}
