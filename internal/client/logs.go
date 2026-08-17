package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	podlogs "github.com/AidarKhusainov/podlaz/internal/logs"
)

const maxDaemonLogsErrorBody = 8 << 10

type LogsClient struct {
	SocketPath  string
	DialTimeout time.Duration
}

func (c LogsClient) Run(ctx context.Context, stdout io.Writer, opts podlogs.Options) error {
	if stdout == nil {
		return errors.New("logs output writer is nil")
	}
	if opts.Since != "" {
		canonical, err := podlogs.ParseSinceDuration(opts.Since)
		if err != nil {
			return err
		}
		opts.Since = canonical
	}

	socketPath := c.SocketPath
	if socketPath == "" {
		socketPath = api.SocketPath("")
	}
	dialTimeout := c.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = defaultStatusTimeout
	}

	err := c.runViaSocket(ctx, stdout, opts, socketPath, dialTimeout)
	if err == nil {
		return nil
	}
	if shouldTryAbstractSocket(socketPath, err) {
		fallbackErr := c.runViaSocket(ctx, stdout, opts, api.AbstractSocketAddress(), dialTimeout)
		if fallbackErr == nil {
			return nil
		}
		return logsAbstractSocketFallbackError(err, fallbackErr)
	}
	return err
}

func (c LogsClient) runViaSocket(ctx context.Context, stdout io.Writer, opts podlogs.Options, socketPath string, dialTimeout time.Duration) error {
	dialer := net.Dialer{Timeout: dialTimeout}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}
	defer transport.CloseIdleConnections()

	requestPath := daemonLogsRequestPath(opts)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://podlazd"+requestPath, nil)
	if err != nil {
		return fmt.Errorf("build daemon logs request: %w", err)
	}
	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return newDaemonUnavailableError(socketPath, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		messageBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, maxDaemonLogsErrorBody))
		if readErr != nil {
			return fmt.Errorf("read daemon logs error response: %w", readErr)
		}
		message := strings.TrimSpace(string(messageBytes))
		if message == "" {
			message = resp.Status
		}
		return fmt.Errorf("daemon logs request failed: %s", message)
	}

	if _, err := io.Copy(stdout, resp.Body); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("read daemon logs stream: %w", err)
	}
	// A follow stream may observe a clean EOF concurrently with caller
	// cancellation. Preserve the CLI signal contract instead of treating that
	// race as a successful end-of-stream.
	if err := ctx.Err(); err != nil {
		return err
	}
	if resp.Trailer.Get(api.LogsErrorTrailer) != "" {
		return errors.New("daemon logs backend failed")
	}
	return nil
}

func logsAbstractSocketFallbackError(filesystemErr, abstractErr error) error {
	if !IsDaemonUnavailable(abstractErr) {
		return abstractErr
	}
	return daemonUnavailableError{
		detail:           "daemon logs IPC is unavailable; podlazd is not listening on the packaged abstract socket. Run `podlaz doctor` or restart podlazd",
		cause:            errors.Join(filesystemErr, abstractErr),
		permissionDenied: errors.Is(filesystemErr, ErrDaemonPermissionDenied),
	}
}

func daemonLogsRequestPath(opts podlogs.Options) string {
	query := url.Values{}
	if opts.Since != "" {
		query.Set("since", opts.Since)
	}
	if opts.Follow {
		query.Set("follow", "1")
	}
	if opts.Core {
		query.Set("core", "1")
	}
	if encoded := query.Encode(); encoded != "" {
		return api.LogsPath + "?" + encoded
	}
	return api.LogsPath
}
