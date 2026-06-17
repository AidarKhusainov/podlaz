//go:build linux

package daemon

import "net"

func peerSubjectFromConn(conn net.Conn) (PeerSubject, bool) {
	return PeerSubject{}, false
}

func readProcStartTime(pid int) (string, error) {
	return "", nil
}
