package doctor

import (
	"context"
	"errors"
	"testing"
)

func TestDNSStateReportsActiveTunWardenRouteOnlyDomain(t *testing.T) {
	runner := fakeRunner{commands: map[string]fakeCommand{
		"resolvectl status tunwarden0 --no-pager": {stdout: "Link 7 (tunwarden0)\n    DNS Domain: ~."},
	}}

	checks := dnsState(context.Background(), runner, "/usr/bin/resolvectl", true)

	assertDNSCheck(t, checks, "dns-backend", SeverityOK, "systemd-resolved per-link DNS inspectable")
	assertDNSCheck(t, checks, "dns-tunwarden-link", SeverityOK, "TunWarden DNS state detected")
}

func TestDNSStateReportsNoTunWardenOwnedDNSState(t *testing.T) {
	runner := fakeRunner{commands: map[string]fakeCommand{
		"resolvectl status tunwarden0 --no-pager": {
			stderr:   "Link tunwarden0 does not exist",
			exitCode: 1,
			err:      errors.New("exit status 1"),
		},
	}}

	checks := dnsState(context.Background(), runner, "/usr/bin/resolvectl", true)

	assertDNSCheck(t, checks, "dns-backend", SeverityOK, "systemd-resolved per-link DNS inspectable")
	assertDNSCheck(t, checks, "dns-tunwarden-link", SeverityOK, "no TunWarden-owned DNS state found")
}

func TestDNSStateWarnsWhenResolvedIsUnavailable(t *testing.T) {
	checks := dnsState(context.Background(), fakeRunner{}, "", false)

	assertDNSCheck(t, checks, "dns-backend", SeverityWarning, "resolvectl not found")
	assertDNSCheck(t, checks, "dns-tunwarden-link", SeverityWarning, "resolvectl is unavailable")
}

func assertDNSCheck(t *testing.T, checks []Check, name string, severity Severity, messageContains string) {
	t.Helper()
	for _, check := range checks {
		if check.Name != name {
			continue
		}
		if check.Severity != severity {
			t.Fatalf("check %s: expected severity %s, got %s", name, severity, check.Severity)
		}
		if !strings.Contains(check.Message, messageContains) {
			t.Fatalf("check %s: expected message containing %q, got %q", name, messageContains, check.Message)
		}
		return
	}
	t.Fatalf("check %s not found in %#v", name, checks)
}
