package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

func (c DoctorClient) TunDiagnostics(ctx context.Context) (tundiag.Report, error) {
	socketPath := c.SocketPath
	if socketPath == "" {
		socketPath = api.SocketPath("")
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 45 * time.Second
	}

	report, err := retryDaemonSocketReadiness(ctx, func(attemptCtx context.Context) (tundiag.Report, error) {
		return c.tunDiagnosticsViaSocket(attemptCtx, socketPath, timeout)
	})
	if err == nil {
		return report, nil
	}
	if shouldTryAbstractSocket(socketPath, err) {
		fallbackReport, fallbackErr := c.tunDiagnosticsViaSocket(ctx, api.AbstractSocketAddress(), timeout)
		if fallbackErr == nil {
			return fallbackReport, nil
		}
		return tundiag.Report{}, abstractSocketFallbackError(err, fallbackErr)
	}
	return tundiag.Report{}, err
}

func (c DoctorClient) tunDiagnosticsViaSocket(ctx context.Context, socketPath string, timeout time.Duration) (tundiag.Report, error) {
	dialer := net.Dialer{Timeout: timeout}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, "unix", socketPath)
	}}
	defer transport.CloseIdleConnections()

	httpClient := http.Client{Transport: transport, Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://podlazd"+api.TunDoctorPath, nil)
	if err != nil {
		return tundiag.Report{}, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return tundiag.Report{}, newDaemonUnavailableError(socketPath, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return tundiag.Report{}, fmt.Errorf("daemon TUN diagnostics request failed: unexpected HTTP status %s", resp.Status)
	}
	var report tundiag.Report
	decoder := json.NewDecoder(resp.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return tundiag.Report{}, fmt.Errorf("daemon TUN diagnostics response was invalid: %w", err)
	}
	if report.SchemaVersion != tundiag.SchemaVersion {
		return tundiag.Report{}, fmt.Errorf("daemon TUN diagnostics response has unsupported schema_version %d", report.SchemaVersion)
	}
	return tundiag.Finalize(report), nil
}
