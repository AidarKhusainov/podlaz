//go:build linux

package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// RunNMOwnedFDBroker serves a single, explicitly started opt-in session.
// A systemd-delegated, root-only service is required. No default daemon path
// calls this method, and a failed/admitted generation is never retried or
// cleaned up by resemblance to observed network state.
func RunNMOwnedFDBroker(ctx context.Context) error {
	if ctx == nil || os.Geteuid() != 0 {
		return errors.New("NM FD broker requires root and a lifecycle context")
	}
	if err := verifyNMGenerationBrokerCapabilities(); err != nil {
		return err
	}
	dir := filepath.Dir(nmBrokerSocket)
	info, err := os.Lstat(dir)
	if err != nil {
		return errors.New("dedicated NM broker runtime directory unavailable")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || stat.Uid != 0 || info.Mode().Perm() != 0o700 {
		return errors.New("NM broker runtime directory has unsafe ownership or permissions")
	}
	// Never unlink an old socket: a residual path after an interrupted service
	// startup must cause a fail-closed operator-visible error.
	if _, err := os.Lstat(nmBrokerSocket); err == nil {
		return errors.New("NM broker socket already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("cannot inspect NM broker socket")
	}
	addr := &net.UnixAddr{Name: nmBrokerSocket, Net: "unixpacket"}
	listener, err := net.ListenUnix("unixpacket", addr)
	if err != nil {
		return fmt.Errorf("listen on private NM broker socket: %w", err)
	}
	defer listener.Close()
	if err := os.Chmod(nmBrokerSocket, 0o600); err != nil {
		return err
	}
	// Close listener when the service is stopped; never accept a second
	// generation after a request, even if the first request was invalid.
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	peer, err := listener.AcceptUnix()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("accept NM broker peer: %w", err)
	}
	defer peer.Close()
	_ = listener.Close()
	go func() {
		<-ctx.Done()
		_ = peer.Close()
	}()
	if err := peer.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	if err := verifyNMBrokerPeer(peer); err != nil {
		return err
	}
	request, fd, err := receiveNMBrokerRequest(peer)
	if err != nil {
		return err
	}
	defer fd.Close()
	if err := peer.SetReadDeadline(time.Time{}); err != nil {
		return err
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		var buffer [1]byte
		// The connection is a lifetime lease, not an additional command
		// channel. EOF, protocol injection or read failure cancels the child.
		_, _ = peer.Read(buffer[:])
		cancel()
	}()
	cgroup, err := createNMBrokerCgroup()
	if err != nil {
		return err
	}
	// The durable deny fence intentionally remains even if a cohort exits
	// cleanly. Do not unlink the cgroup if it could still contain processes.
	supervisor := nmConfinedXrayGeneration{
		l3: newNML3Stager("/run/podlaz", nil),
		generation: newNMGenerationFence("/run/podlaz", nil),
	}
	return supervisor.runWithStart(childCtx, fd, request.Kernel, request.NM,
		request.SessionID, request.EgressMark, request.ConfigPath, cgroup, func() error {
			_, err := peer.Write([]byte("spawned"))
			return err
		})
}

func verifyNMBrokerPeer(peer *net.UnixConn) error {
	if peer == nil {
		return errors.New("NM broker peer unavailable")
	}
	raw, err := peer.SyscallConn()
	if err != nil {
		return err
	}
	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil || credErr != nil || cred == nil || cred.Uid != 0 || cred.Pid <= 1 {
		return errors.New("NM broker requires a privileged local Unix peer")
	}
	return nil
}

func receiveNMBrokerRequest(peer *net.UnixConn) (nmBrokerGenerationRequest, *os.File, error) {
	if peer == nil {
		return nmBrokerGenerationRequest{}, nil, errors.New("NM broker peer unavailable")
	}
	msg := make([]byte, nmBrokerRequestMaxBytes+1)
	oob := make([]byte, unix.CmsgSpace(4*8))
	n, oobn, flags, _, err := peer.ReadMsgUnix(msg, oob)
	if err != nil {
		return nmBrokerGenerationRequest{}, nil, errors.New("NM broker request unavailable")
	}
	var received []int
	messages, parseErr := unix.ParseSocketControlMessage(oob[:oobn])
	if parseErr == nil {
		for _, cm := range messages {
			if cm.Header.Level != unix.SOL_SOCKET || cm.Header.Type != unix.SCM_RIGHTS {
				parseErr = errors.New("unexpected NM broker ancillary message")
				break
			}
			fds, e := unix.ParseUnixRights(&cm)
			if e != nil {
				parseErr = e
				break
			}
			received = append(received, fds...)
		}
	}
	// Every received descriptor must be closed, including malformed and
	// extra-fd requests. Never allow any unowned copy to remain in the broker.
	if parseErr != nil || flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 || len(received) != 1 {
		for _, fd := range received {
			_ = unix.Close(fd)
		}
		return nmBrokerGenerationRequest{}, nil, errors.New("NM broker requires exactly one TUN FD")
	}
	file := os.NewFile(uintptr(received[0]), "inherited NM TUN")
	request, err := decodeNMBrokerGenerationRequest(msg[:n])
	if err != nil {
		file.Close()
		return nmBrokerGenerationRequest{}, nil, err
	}
	return request, file, nil
}

// Only the dedicated systemd service may create a child cgroup under its
// explicitly delegated subtree. This never writes systemd-owned parent nodes.
func createNMBrokerCgroup() (string, error) {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil || len(data) > 4096 {
		return "", errors.New("NM broker cgroup delegation unavailable")
	}
	const prefix = "0::"
	var own string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, prefix) {
			if own != "" {
				return "", errors.New("ambiguous NM broker cgroup")
			}
			own = strings.TrimPrefix(line, prefix)
		}
	}
	if !strings.HasSuffix(own, "/podlaz-nm-fd-broker.service") ||
		!filepath.IsAbs(own) || filepath.Clean(own) != own {
		return "", errors.New("NM broker is not running in dedicated systemd cgroup")
	}
	path := filepath.Join("/sys/fs/cgroup", own, "xray-generation")
	if err := os.Mkdir(path, 0o700); err != nil {
		return "", fmt.Errorf("create delegated NM Xray cohort: %w", err)
	}
	return path, nil
}
