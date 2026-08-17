//go:build !linux

package daemon

import "fmt"

func authorizeDaemonLogsPeer(PeerSubject) error {
	return fmt.Errorf("%w: daemon log peer-group authorization is supported only on Linux", ErrAuthorizationUnavailable)
}
