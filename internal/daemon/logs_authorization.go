package daemon

import "fmt"

// authorizeDaemonLogsPeer keeps the privileged journal read behind an
// authenticated local Unix peer. Unlike lifecycle mutations it intentionally
// does not require polkit or membership in the daemon's private filesystem
// socket group: the output is read-only and passes through the product redactor,
// matching the existing ordinary-user status/doctor diagnostics boundary.
func authorizeDaemonLogsPeer(subject PeerSubject) error {
	if subject.PID <= 0 || subject.StartTime == 0 {
		return fmt.Errorf("%w: daemon could not establish the local logs peer identity", ErrAuthorizationUnavailable)
	}
	return nil
}
