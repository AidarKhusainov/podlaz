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
	steps := successfulAddressVerifyCommands(7)
	steps = append(steps, commandResult(
		[]string{"resolvectl", "--cache=no", "--interface=7", "-4", "query", "example.com"},
		CommandResult{Stdout: "example.com: 93.184.216.34 -- link: podlaz0\n"},
		nil,
	))
	steps = append(steps, linkCommandResult(8, "", nil))
	runner := &scriptedRunner{t: t, steps: steps}

	_, err := (TunDNSReadinessVerifier{Runner: runner}).VerifyScoped(context.Background(), plan, "example.com")
	if err == nil || !errors.Is(err, ErrResolvedLinkQueryFailure) || !errors.Is(err, ErrTunLinkIdentityMismatch) {
		t.Fatalf("expected replacement-race scoped-query failure, got %v", err)
	}
	runner.assertDone()
}

func TestTunDNSReadinessVerifierRequiresExactAddressBeforeScopedQuery(t *testing.T) {
	plan := boundAddressPlanForTest()
	runner := &scriptedRunner{t: t, steps: []scriptedCommand{
		linkCommandResult(7, "", nil),
		addressCommandResult(""),
		linkCommandResult(7, "", nil),
	}}

	_, err := (TunDNSReadinessVerifier{Runner: runner}).VerifyScoped(context.Background(), plan, "example.com")
	if err == nil || !errors.Is(err, ErrResolvedLinkNotReady) {
		t.Fatalf("expected missing-address readiness failure, got %v", err)
	}
	runner.assertDone()
}

func TestTunDNSReadinessVerifierRevalidatesIdentityThenQueriesResolved(t *testing.T) {
	plan := boundAddressPlanForTest()
	steps := successfulAddressVerifyCommands(7)
	steps = append(steps, commandResult(
		[]string{"resolvectl", "--cache=no", "--interface=7", "-4", "query", "example.com"},
		CommandResult{Stdout: "example.com: 93.184.216.34 -- link: podlaz0\n"},
		nil,
	))
	steps = append(steps, successfulAddressVerifyCommands(7)...)
	runner := &scriptedRunner{t: t, steps: steps}

	addresses, err := (TunDNSReadinessVerifier{Runner: runner}).VerifyScoped(context.Background(), plan, "example.com")
	if err != nil {
		t.Fatalf("verify scoped DNS readiness: %v", err)
	}
	if !reflect.DeepEqual(addresses, []string{"93.184.216.34"}) {
		t.Fatalf("unexpected scoped answers: %#v", addresses)
	}
	runner.assertDone()
}

func successfulAddressVerifyCommands(index int) []scriptedCommand {
	return []scriptedCommand{
		linkCommandResult(index, "", nil),
		addressCommandResult(addressLineForTest(index, planner.DefaultTunIPv4CIDR)),
		linkCommandResult(index, "", nil),
		linkCommandResult(index, "", nil),
	}
}
