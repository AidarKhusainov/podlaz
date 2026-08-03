package executor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

const maxResolvedScopedQueryOutput = 64 * 1024

var (
	ErrResolvedLinkNotReady     = errors.New("systemd-resolved link is not ready")
	ErrResolvedLinkQueryFailure = errors.New("systemd-resolved scoped query failure")
)

// ResolvedScopedQueryVerifier performs one uncached IPv4 lookup bound to the
// exact systemd-resolved link. It accepts an answer only when resolvectl
// identifies the expected link in the result itself.
type ResolvedScopedQueryVerifier struct {
	Runner CommandRunner
}

func (e ResolvedScopedQueryVerifier) Query(ctx context.Context, link, name string) ([]string, error) {
	link = strings.TrimSpace(link)
	name = strings.TrimSpace(name)
	if link == "" {
		return nil, fmt.Errorf("%w: missing target link", ErrResolvedLinkQueryFailure)
	}
	if name == "" {
		return nil, fmt.Errorf("%w: missing query name", ErrResolvedLinkQueryFailure)
	}

	result, err := observeCommand(ctx, e.Runner, "resolvectl", "--cache=no", "--interface="+link, "-4", "query", name)
	if err != nil {
		return nil, fmt.Errorf("%w: query %s through %s: %w", ErrResolvedLinkQueryFailure, name, link, err)
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
		return nil, fmt.Errorf("%w: resolvectl output exceeds %d bytes", ErrResolvedLinkQueryFailure, maxResolvedScopedQueryOutput)
	}
	if strings.TrimSpace(rawStderr) != "" {
		return nil, fmt.Errorf("%w: resolvectl wrote unexpected stderr", ErrResolvedLinkQueryFailure)
	}
	addresses, err := parseResolvedScopedIPv4Answers(rawStdout, link)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrResolvedLinkQueryFailure, err)
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
	return (ResolvedScopedQueryVerifier{Runner: e.Runner}).Query(ctx, plan.Interface, name)
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
		return nil, fmt.Errorf("scoped query result does not identify expected link %s", expectedLink)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("scoped query through %s returned no IPv4 answers", expectedLink)
	}
	return addresses, nil
}
