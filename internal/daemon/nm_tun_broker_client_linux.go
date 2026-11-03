//go:build linux

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// nmBrokerLease is an explicit lifetime lease, not a connectivity verdict.
// Close requests bounded cohort termination; it does not clear the durable
// NM generation or authorize deletion of NM L3, nftables or foreign state.
type nmBrokerLease struct {
	conn *net.UnixConn
	once sync.Once
	done chan struct{}
}

// Done closes if the dedicated broker process disconnects, even if the
// caller never explicitly disconnected. It is not a VPN connectivity status.
func (l *nmBrokerLease) Done() <-chan struct{} {
	if l == nil {
		return nil
	}
	return l.done
}

func (l *nmBrokerLease) Close() error {
	if l == nil || l.conn == nil {
		return nil
	}
	var closeErr error
	l.once.Do(func() { closeErr = l.conn.Close() })
	return closeErr
}

// requestNMConfinedXrayGeneration sends exactly one TUN FD to the separately
// opted-in, root-only broker. The caller must have staged *and verified* the
// exact protected NM L3 intent and the pinned official Xray config in advance.
// The broker repeats identity/envelope/generation checks independently.
// It consumes fd on every outcome; no copied packet FD stays with the caller.
func requestNMConfinedXrayGeneration(ctx context.Context, fd *os.File, request nmBrokerGenerationRequest) (*nmBrokerLease, error) {
	if fd == nil {
		return nil, errors.New("NM broker requires a TUN descriptor")
	}
	defer fd.Close()
	if ctx == nil || ctx.Err() != nil || fd.Fd() < 3 {
		return nil, errors.New("NM broker call has no live context or descriptor")
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, errors.New("NM broker request encoding failed")
	}
	if _, err := decodeNMBrokerGenerationRequest(payload); err != nil {
		return nil, err
	}
	var dialer net.Dialer
	connected, err := dialer.DialContext(ctx, "unixpacket", nmBrokerSocket)
	if err != nil {
		return nil, errors.New("dedicated NM broker is unavailable; normal full-TUN backend remains available")
	}
	conn, ok := connected.(*net.UnixConn)
	if !ok {
		connected.Close()
		return nil, errors.New("NM broker has an unexpected transport")
	}
	deadline := time.Now().Add(10 * time.Second)
	if deadlineCtx, ok := ctx.Deadline(); ok && deadlineCtx.Before(deadline) {
		deadline = deadlineCtx
	}
	if err := conn.SetDeadline(deadline); err != nil {
		conn.Close()
		return nil, err
	}
	n, _, err := conn.WriteMsgUnix(payload, unix.UnixRights(int(fd.Fd())), nil)
	if err != nil || n != len(payload) {
		conn.Close()
		return nil, errors.New("NM broker FD handoff failed")
	}
	// The original sender descriptor is closed immediately after SCM_RIGHTS
	// has transferred a kernel reference. The broker closes its copy again
	// after clone-into-cgroup handoff.
	if err := fd.Close(); err != nil {
		conn.Close()
		return nil, errors.New("NM broker sender FD release failed")
	}
	reply := make([]byte, 16)
	n, err = conn.Read(reply)
	if err != nil && !errors.Is(err, io.EOF) {
		conn.Close()
		return nil, errors.New("NM broker launch not acknowledged")
	}
	if string(reply[:n]) != "spawned" {
		conn.Close()
		return nil, errors.New("NM broker refused generation admission")
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, err
	}
	lease := &nmBrokerLease{conn: conn, done: make(chan struct{})}
	// Watch the broker's lifetime independently from caller cancellation. A
	// sudden child crash must not leave the caller believing the lease lives.
	go func() {
		defer close(lease.done)
		var unexpected [16]byte
		_, _ = conn.Read(unexpected[:])
		_ = lease.Close()
	}()
	go func() {
		select {
		case <-ctx.Done():
			_ = lease.Close()
		case <-lease.done:
		}
	}()
	return lease, nil
}
