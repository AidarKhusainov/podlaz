//go:build linux

package daemon

import (
	"net"
	"syscall"
)

func peerSubjectFromConn(conn net.Conn) (PeerSubject, bool) {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return PeerSubject{}, false
	}
	rawConn, err := unixConn.SyscallConn()
	if err != nil {
		return PeerSubject{}, false
	}
	var subject PeerSubject
	var peerErr error
	controlErr := rawConn.Control(func(fd uintptr) {
		cred, err := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		if err != nil {
			peerErr = err
			return
		}
		subject = PeerSubject{PID: int(cred.Pid), UID: cred.Uid, GID: cred.Gid}
	})
	if controlErr != nil || peerErr != nil || subject.PID <= 0 {
		return PeerSubject{}, false
	}
	startTime, err := readPeerProcessStartTime(subject.PID)
	if err != nil {
		return PeerSubject{}, false
	}
	subject.StartTime = startTime
	return subject, true
}
