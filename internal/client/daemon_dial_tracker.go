package client

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"time"
)

type daemonDialTracker struct {
	socketPath string
	dialer     net.Dialer
	connected  atomic.Bool
}

func newDaemonDialTracker(socketPath string, timeout time.Duration) *daemonDialTracker {
	return &daemonDialTracker{
		socketPath: socketPath,
		dialer:     net.Dialer{Timeout: timeout},
	}
}

func (d *daemonDialTracker) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	conn, err := d.dialer.DialContext(ctx, "unix", d.socketPath)
	if err == nil {
		d.connected.Store(true)
	}
	return conn, err
}

func (d *daemonDialTracker) requestError(operation string, err error) error {
	if d == nil || !d.connected.Load() {
		return newDaemonUnavailableError(d.socketPath, err)
	}
	return fmt.Errorf("daemon %s request failed after connection: %w", operation, err)
}
