package daemon

import (
	"errors"
	"fmt"
	"strings"
)

type TunVerificationError struct {
	Phase             string
	Summary           string
	Diagnostics       []string
	RollbackCompleted bool
	err               error
}

func newTunVerificationError(phase, summary string, err error) *TunVerificationError {
	return &TunVerificationError{Phase: strings.TrimSpace(phase), Summary: strings.TrimSpace(summary), err: err}
}

func (e *TunVerificationError) Error() string {
	if e == nil {
		return "Unable to connect.\n\nPodlaz could not verify the VPN connection.\n\nRun:\n  podlaz debug doctor --tun"
	}

	reason := "Podlaz could not verify the VPN connection."
	switch strings.TrimSpace(e.Phase) {
	case "resolved-link", "resolved-link-query", "system-resolver", "dns-route", "dns", "dns-udp", "dns-tcp":
		reason = "Podlaz could not verify DNS through the VPN."
	case "route", "tcp", "https", "tls":
		reason = "Podlaz could not verify full-VPN connectivity."
	}

	var b strings.Builder
	b.WriteString("Unable to connect.\n\n")
	b.WriteString(reason)
	b.WriteString("\n")
	if e.RollbackCompleted {
		b.WriteString("The attempted Podlaz-owned network changes were rolled back.\n")
	} else {
		b.WriteString("Podlaz did not publish an active VPN session.\n")
	}
	b.WriteString("\nRun:\n  podlaz debug doctor --tun")
	return b.String()
}

func (e *TunVerificationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func withTunRollbackCompleted(err error) error {
	if err == nil {
		return nil
	}

	completed := err
	var verification *TunVerificationError
	if errors.As(err, &verification) {
		copy := *verification
		copy.RollbackCompleted = true
		completed = &copy
	} else {
		completed = fmt.Errorf("%w; rolled back applied podlaz-owned networking state", err)
	}

	// Rollback completion proves the candidate mutation outcome only. It never
	// creates replay terminality. Preserve a positive typed disposition when the
	// underlying cause already carries one; otherwise remain conservatively
	// incomplete while recording that exact candidate rollback completed.
	var semantic networkSessionReplaySemanticsError
	if errors.As(err, &semantic) && validNetworkSessionReplayDisposition(semantic.disposition) {
		return withNetworkSessionReplaySemantics(
			semantic.disposition,
			networkSessionCandidateMutationRolledBack,
			completed,
		)
	}
	return withNetworkSessionReplaySemantics(
		networkSessionReplayDispositionIncomplete,
		networkSessionCandidateMutationRolledBack,
		completed,
	)
}

func isTunVerificationError(err error) bool {
	var verification *TunVerificationError
	return errors.As(err, &verification)
}
