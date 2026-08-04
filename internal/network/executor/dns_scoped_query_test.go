package executor

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestResolvedScopedQueryVerifierUsesUncachedInterfaceBoundIPv4Query(t *testing.T) {
	runner := &recordingRunner{stdout: "example.com: 93.184.216.34 93.184.216.35 -- link: podlaz0\n"}

	addresses, err := (ResolvedScopedQueryVerifier{Runner: runner}).Query(context.Background(), "podlaz0", 7, "example.com")
	if err != nil {
		t.Fatalf("scoped query: %v", err)
	}
	if !reflect.DeepEqual(addresses, []string{"93.184.216.34", "93.184.216.35"}) {
		t.Fatalf("unexpected scoped IPv4 answers: %#v", addresses)
	}
	want := [][]string{{"resolvectl", "--cache=no", "--interface=7", "-4", "query", "example.com"}}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("unexpected scoped query argv:\nwant %#v\n got %#v", want, runner.commands)
	}
}

func TestResolvedScopedQueryVerifierRejectsAnswerWithoutExpectedLinkEvidence(t *testing.T) {
	runner := &recordingRunner{stdout: "example.com: 93.184.216.34 -- link: wlan0\n"}

	_, err := (ResolvedScopedQueryVerifier{Runner: runner}).Query(context.Background(), "podlaz0", 7, "example.com")
	if err == nil || !errors.Is(err, ErrResolvedLinkQueryFailure) || !strings.Contains(err.Error(), "podlaz0") {
		t.Fatalf("expected exact-link scoped-query failure, got %v", err)
	}
}

func TestTunDNSReadinessVerifierRejectsReplacementDuringScopedQuery(t *testing.T) {
	plan := boundAddressPlanForTest()
	runner := &scriptedRunner{t: t, steps: []scriptedCommand{
		{want: []string{"ip", "-details", "-o", "link", "show", "dev", "podlaz0"}, result: CommandResult{Stdout: tunLinkDetailsForAddressTest(7, true)}},
		{want: []string{"ip", "-4", "-o", "address", "show", "dev", "podlaz0"}, result: CommandResult{Stdout: addressLineForTest(7, planner.DefaultTunIPv4CIDR)}},
		{want: []string{"resolvectl", "--cache=no", "--interface=7", "-4", "query", "example.com"}, result: CommandResult{Stdout: "example.com: 93.184.216.34 -- link: podlaz0\n"}},
		{want: []string{"ip", "-details", "-o", "link", "show", "dev", "podlaz0"}, result: CommandResult{Stdout: tunLinkDetailsForAddressTest(8, true)}},
	}}

	_, err := (TunDNSReadinessVerifier{Runner: runner}).VerifyScoped(context.Background(), plan, "example.com")
	if err == nil || !errors.Is(err, ErrResolvedLinkQueryFailure) || !errors.Is(err, ErrTunLinkIdentityMismatch) {
		t.Fatalf("expected replacement-race scoped-query failure, got %v", err)
	}
	runner.assertDone()
}

func TestTunDNSReadinessVerifierRequiresExactAddressBeforeScopedQuery(t *testing.T) {
	plan := boundAddressPlanForTest()
	runner := &scriptedRunner{t: t, steps: []scriptedCommand{
		{want: []string{"ip", "-details", "-o", "link", "show", "dev", "podlaz0"}, result: CommandResult{Stdout: tunLinkDetailsForAddressTest(7, true)}},
		{want: []string{"ip", "-4", "-o", "address", "show", "dev", "podlaz0"}},
	}}

	_, err := (TunDNSReadinessVerifier{Runner: runner}).VerifyScoped(context.Background(), plan, "example.com")
	if err == nil || !errors.Is(err, ErrResolvedLinkNotReady) {
		t.Fatalf("expected missing-address readiness failure, got %v", err)
	}
	runner.assertDone()
}

func TestTunDNSReadinessVerifierRevalidatesIdentityThenQueriesResolved(t *testing.T) {
	plan := boundAddressPlanForTest()
	runner := &scriptedRunner{t: t, steps: []scriptedCommand{
		{want: []string{"ip", "-details", "-o", "link", "show", "dev", "podlaz0"}, result: CommandResult{Stdout: tunLinkDetailsForAddressTest(7, true)}},
		{want: []string{"ip", "-4", "-o", "address", "show", "dev", "podlaz0"}, result: CommandResult{Stdout: "7: podlaz0    inet " + planner.DefaultTunIPv4CIDR + " scope global podlaz0"}},
		{want: []string{"resolvectl", "--cache=no", "--interface=7", "-4", "query", "example.com"}, result: CommandResult{Stdout: "example.com: 93.184.216.34 -- link: podlaz0\n"}},
		{want: []string{"ip", "-details", "-o", "link", "show", "dev", "podlaz0"}, result: CommandResult{Stdout: tunLinkDetailsForAddressTest(7, true)}},
		{want: []string{"ip", "-4", "-o", "address", "show", "dev", "podlaz0"}, result: CommandResult{Stdout: "7: podlaz0    inet " + planner.DefaultTunIPv4CIDR + " scope global podlaz0"}},
	}}

	addresses, err := (TunDNSReadinessVerifier{Runner: runner}).VerifyScoped(context.Background(), plan, "example.com")
	if err != nil {
		t.Fatalf("verify scoped DNS readiness: %v", err)
	}
	if !reflect.DeepEqual(addresses, []string{"93.184.216.34"}) {
		t.Fatalf("unexpected scoped answers: %#v", addresses)
	}
	runner.assertDone()
}
