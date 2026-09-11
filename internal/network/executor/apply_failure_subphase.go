package executor

import (
	"errors"
	"strings"
)

const (
	applyFailureSubphaseTunAddress  = "tun-address"
	applyFailureSubphaseRoutes      = "routes"
	applyFailureSubphasePolicyRules = "policy-rules"
	applyFailureSubphaseDNS         = "dns"
	applyFailureSubphaseNFTables    = "nftables"
)

// applyFailureSubphaseError carries the exact apply boundary that failed while
// preserving the underlying error text and errors.Is/errors.As behavior.
type applyFailureSubphaseError struct {
	subphase string
	err      error
}

func (e applyFailureSubphaseError) Error() string {
	if e.err == nil {
		return "TUN apply failed"
	}
	return e.err.Error()
}

func (e applyFailureSubphaseError) Unwrap() error { return e.err }

func withApplyFailureSubphase(subphase string, err error) error {
	if err == nil {
		return nil
	}
	subphase = strings.TrimSpace(subphase)
	if subphase == "" {
		return err
	}
	return applyFailureSubphaseError{subphase: subphase, err: err}
}

// ApplyFailureSubphase returns bounded typed apply evidence. It never infers a
// subphase from human-readable error text.
func ApplyFailureSubphase(err error) string {
	var phased applyFailureSubphaseError
	if !errors.As(err, &phased) {
		return ""
	}
	return phased.subphase
}
