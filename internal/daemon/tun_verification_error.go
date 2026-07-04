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
		return "podlaz: TUN verification failed"
	}
	phase := e.Phase
	if phase == "" {
		phase = "connectivity"
	}
	summary := e.Summary
	if summary == "" && e.err != nil {
		summary = e.err.Error()
	}
	if summary == "" {
		summary = "TUN connectivity verification failed."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "podlaz: TUN connection failed during %s verification.\n\n", phase)
	b.WriteString(summary)
	if !strings.HasSuffix(summary, ".") {
		b.WriteString(".")
	}
	b.WriteString("\n")
	if e.RollbackCompleted {
		b.WriteString("Rollback completed; no podlaz-owned network changes were left applied.\n")
	}
	if len(e.Diagnostics) > 0 {
		b.WriteString("\nDiagnostics:\n")
		for _, diagnostic := range e.Diagnostics {
			diagnostic = strings.TrimSpace(diagnostic)
			if diagnostic != "" {
				b.WriteString("  ")
				b.WriteString(diagnostic)
				b.WriteString("\n")
			}
		}
	}
	b.WriteString("\nRun:\n  plz doctor\n  plz plan --mode tun <profile> --verbose\n  podlaz logs --core")
	return b.String()
}

func (e *TunVerificationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func withTunRollbackCompleted(err error) error {
	var verification *TunVerificationError
	if errors.As(err, &verification) {
		copy := *verification
		copy.RollbackCompleted = true
		return &copy
	}
	return fmt.Errorf("%w; rolled back applied podlaz-owned networking state", err)
}

func isTunVerificationError(err error) bool {
	var verification *TunVerificationError
	return errors.As(err, &verification)
}
