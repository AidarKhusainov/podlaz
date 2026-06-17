//go:build !linux

package daemon

import "net"

func peerSubjectFromConn(net.Conn) (PeerSubject, bool) {
	return PeerSubject{}, false
}
