package executor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

const maxResolvedScopedQueryOutput = 64 * 1024

var (
	ErrResolvedLinkNotReady     = errors.New("systemd-resolved link is not ready")
	ErrResolvedLinkQueryFailure = errors.New("systemd-resolved scoped query failure")
	errResolvedScopedWrongLink  = errors.New("scoped query result does not identify expected link")
	errResolvedScopedNoIPv4     = errors.New("scoped query returned no IPv4 answers")
)

// ResolvedScopedQueryFailureCause is diagnostic evidence only, not a permission
// to retry a failed Network Session or infer cleanup authority.
const (
	ResolvedScopedCauseInvalidInput       = "invalid-input"
	ResolvedScopedCauseCommandTimeout     = "command-timeout"
	ResolvedScopedCauseCommandUnavailable = "command-unavailable"
	ResolvedScopedCauseCommandFailure     = "command-failure"
	ResolvedScopedCauseOutputTooLarge     = "output-too-large"
	ResolvedScopedCauseUnexpectedStderr   = "unexpected-stderr"
	ResolvedScopedCauseLinkNotMatched     = "link-not-matched"
	ResolvedScopedCauseNoIPv4             = "no-ipv4"
	ResolvedScopedCauseIdentityChanged    = "identity-changed"
)

type resolvedScopedQueryCauseError struct {
	cause string
	err   error
}

func (e resolvedScopedQueryCauseError) Error() string { return e.err.Error() }
func (e resolvedScopedQueryCauseError) Unwrap() error { return e.err }

func validResolvedScopedQueryFailureCause(cause string) bool {
	switch cause {
	case ResolvedScopedCauseInvalidInput, ResolvedScopedCauseCommandTimeout,
		ResolvedScopedCauseCommandUnavailable, ResolvedScopedCauseCommandFailure,
		ResolvedScopedCauseOutputTooLarge, ResolvedScopedCauseUnexpectedStderr,
		ResolvedScopedCauseLinkNotMatched, ResolvedScopedCauseNoIPv4,
		ResolvedScopedCauseIdentityChanged:
		return true
	default:
		return false
	}
}

func ValidResolvedScopedQueryFailureCause(cause string) bool {
	return validResolvedScopedQueryFailureCause(cause)
}

func ResolvedScopedQueryFailureCause(err error) string {
	var classified resolvedScopedQueryCauseError
	if errors.As(err, &classified) && validResolvedScopedQueryFailureCause(classified.cause) {
		return classified.cause
	}
	return ""
}

func resolvedScopedQueryFailure(cause string, err error) error {
	return resolvedScopedQueryCauseError{cause: cause, err: err}
}

// ResolvedScopedQueryVerifier performs one uncached IPv4 lookup bound to the
// exact systemd-resolved link. It accepts an answer only when resolvectl
// identifies the expected link in the result itself.
type ResolvedScopedQueryVerifier struct {
	Runner CommandRunner
}

func (e ResolvedScopedQueryVerifier) Query(ctx context.Context, link string, linkIndex int, name string) ([]string, error) {
	link = strings.TrimSpace(link)
	name = strings.TrimSpace(name)
	if link == "" {
		return nil, resolvedScopedQueryFailure(ResolvedScopedCauseInvalidInput, fmt.Errorf("%w: missing target link", ErrResolvedLinkQueryFailure))
	}
	if linkIndex <= 0 {
		return nil, resolvedScopedQueryFailure(ResolvedScopedCauseInvalidInput, fmt.Errorf("%w: missing target link index", ErrResolvedLinkQueryFailure))
	}
	if name == "" {
		return nil, resolvedScopedQueryFailure(ResolvedScopedCauseInvalidInput, fmt.Errorf("%w: missing query name", ErrResolvedLinkQueryFailure))
	}

	result, err := observeCommand(ctx, e.Runner, "resolvectl", "--cache=no", "--interface="+strconv.Itoa(linkIndex), "-4", "query", name)
	if err != nil {
		cause := ResolvedScopedCauseCommandFailure
		var unavailable *exec.Error
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
			cause = ResolvedScopedCauseCommandTimeout
		case errors.As(err, &unavailable):
			cause = ResolvedScopedCauseCommandUnavailable
		}
		return nil, resolvedScopedQueryFailure(cause, fmt.Errorf("%w: query %s through %s: %w", ErrResolvedLinkQueryFailure, name, link, err))
	}
	rawStdout := result.RawStdout
	if rawStdout == "" {
		rawStdout = result.Stdout
	}
	rawStderr := result.RawStderr
	if rawStderr == "" {
		rawStderr = result.Stderr
	}
	if len(rawStdout) > maxResolvedScopedQueryOutput || len(rawStderr) > maxResolvedScopedQueryOutput {
		return nil, resolvedScopedQueryFailure(ResolvedScopedCauseOutputTooLarge, fmt.Errorf("%w: resolvectl output exceeds %d bytes", ErrResolvedLinkQueryFailure, maxResolvedScopedQueryOutput))
	}
	if strings.TrimSpace(rawStderr) != "" {
		return nil, resolvedScopedQueryFailure(ResolvedScopedCauseUnexpectedStderr, fmt.Errorf("%w: resolvectl wrote unexpected stderr", ErrResolvedLinkQueryFailure))
	}
	addresses, err := parseResolvedScopedIPv4Answers(rawStdout, link)
	if err != nil {
		cause := ResolvedScopedCauseLinkNotMatched
		if errors.Is(err, errResolvedScopedNoIPv4) {
			cause = ResolvedScopedCauseNoIPv4
		}
		return nil, resolvedScopedQueryFailure(cause, fmt.Errorf("%w: %w", ErrResolvedLinkQueryFailure, err))
	}
	return addresses, nil
}

// TunDNSReadinessVerifier revalidates the exact Xray-created link identity and
// daemon-owned address immediately before the functional resolved lookup.
type TunDNSReadinessVerifier struct {
	Runner CommandRunner
}

func (e TunDNSReadinessVerifier) VerifyScoped(ctx context.Context, plan planner.TunAddressPlan, name string) ([]string, error) {
	if err := (IPTunAddressExecutor{Runner: e.Runner}).Verify(ctx, plan); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrResolvedLinkNotReady, err)
	}
	addresses, err := (ResolvedScopedQueryVerifier{Runner: e.Runner}).Query(ctx, plan.Interface, plan.LinkIndex, name)
	if err != nil {
		return nil, err
	}
	if err := (IPTunAddressExecutor{Runner: e.Runner}).Verify(ctx, plan); err != nil {
		return nil, resolvedScopedQueryFailure(ResolvedScopedCauseIdentityChanged, fmt.Errorf("%w: TUN link identity changed during scoped query: %w", ErrResolvedLinkQueryFailure, err))
	}
	return addresses, nil
}

func parseResolvedScopedIPv4Answers(output, expectedLink string) ([]string, error) {
	expectedLink = strings.TrimSpace(expectedLink)
	seenLink := false
	seen := make(map[string]struct{})
	var addresses []string
	for _, rawLine := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(rawLine)
		const marker = " -- link: "
		idx := strings.LastIndex(line, marker)
		if idx < 0 {
			continue
		}
		linkFields := strings.Fields(strings.TrimSpace(line[idx+len(marker):]))
		if len(linkFields) == 0 || linkFields[0] != expectedLink {
			continue
		}
		seenLink = true
		answer := line[:idx]
		if colon := strings.IndexByte(answer, ':'); colon >= 0 {
			answer = answer[colon+1:]
		}
		for _, token := range strings.Fields(answer) {
			candidate := strings.Trim(token, "[](),;")
			ip := net.ParseIP(candidate)
			if ip == nil || ip.To4() == nil {
				continue
			}
			value := ip.To4().String()
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			addresses = append(addresses, value)
		}
	}
	if !seenLink {
		return nil, fmt.Errorf("%w %s", errResolvedScopedWrongLink, expectedLink)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("%w through %s", errResolvedScopedNoIPv4, expectedLink)
	}
	return addresses, nil
}
