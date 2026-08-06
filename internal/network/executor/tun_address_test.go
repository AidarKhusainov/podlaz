package executor

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

type scriptedCommand struct {
	want   []string
	result CommandResult
	err    error
}

type scriptedRunner struct {
	t      *testing.T
	steps  []scriptedCommand
	called [][]string
}

func (r *scriptedRunner) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	r.t.Helper()
	got := append([]string{name}, args...)
	r.called = append(r.called, got)
	if len(r.steps) == 0 {
		r.t.Fatalf("unexpected command: %#v", got)
	}
	step := r.steps[0]
	r.steps = r.steps[1:]
	if !reflect.DeepEqual(got, step.want) {
		r.t.Fatalf("unexpected command:\nwant %#v\n got %#v", step.want, got)
	}
	return withRawTestCommandOutput(step.result), step.err
}

func (r *scriptedRunner) assertDone() {
	r.t.Helper()
	if len(r.steps) != 0 {
		r.t.Fatalf("%d scripted command(s) were not called", len(r.steps))
	}
}

func TestIPTunAddressBindWaitsForTrackedXrayLinkAndRecordsIdentity(t *testing.T) {
	missing := errors.New("exit status 1")
	runner := &scriptedRunner{t: t, steps: []scriptedCommand{
		linkCommandResult(1, `Device "podlaz0" does not exist.`, missing),
		linkCommandResult(7, "", nil),
		linkCommandResult(7, "", nil),
	}}
	exec := IPTunAddressExecutor{Runner: runner, BindAttempts: 2, BindPollInterval: time.Nanosecond, Sleep: func(context.Context, time.Duration) error { return nil }}

	bound, err := exec.Bind(context.Background(), planner.TunAddressPlan{Interface: "podlaz0", CIDR: planner.DefaultTunIPv4CIDR, LinkKind: "tun"}, liveTunLinkProofForTest())
	if err != nil {
		t.Fatalf("bind TUN address identity: %v", err)
	}
	if bound.LinkIndex != 7 || bound.LinkKind != "tun" || !bound.AppearedAfterCore {
		t.Fatalf("unexpected bound identity: %#v", bound)
	}
	runner.assertDone()
}

func TestIPTunAddressBindAcceptsFirstProbeExistingOnlyWithPreStartAbsenceProof(t *testing.T) {
	runner := &scriptedRunner{t: t, steps: []scriptedCommand{
		linkCommandResult(7, "", nil),
		linkCommandResult(7, "", nil),
	}}
	bound, err := (IPTunAddressExecutor{Runner: runner, BindAttempts: 1}).Bind(context.Background(), planner.TunAddressPlan{Interface: "podlaz0", CIDR: planner.DefaultTunIPv4CIDR}, liveTunLinkProofForTest())
	if err != nil {
		t.Fatalf("bind first-probe-existing TUN link with pre-start proof: %v", err)
	}
	if bound.LinkIndex != 7 || !bound.AppearedAfterCore {
		t.Fatalf("unexpected bound identity: %#v", bound)
	}
	runner.assertDone()
}

func TestIPTunAddressBindRejectsMissingPreStartAbsenceProof(t *testing.T) {
	runner := &scriptedRunner{t: t}
	proof := liveTunLinkProofForTest()
	proof.PreStartAbsent = false
	_, err := (IPTunAddressExecutor{Runner: runner}).Bind(context.Background(), planner.TunAddressPlan{Interface: "podlaz0", CIDR: planner.DefaultTunIPv4CIDR}, proof)
	if err == nil || !errors.Is(err, ErrTunAddressVerify) || !strings.Contains(err.Error(), "not authoritatively absent") {
		t.Fatalf("expected missing pre-start absence proof failure, got %v", err)
	}
}

func TestIPTunAddressBindRejectsReplacementDuringIdentityConfirmation(t *testing.T) {
	runner := &scriptedRunner{t: t, steps: []scriptedCommand{
		linkCommandResult(7, "", nil),
		linkCommandResult(8, "", nil),
	}}
	_, err := (IPTunAddressExecutor{Runner: runner, BindAttempts: 1}).Bind(context.Background(), planner.TunAddressPlan{Interface: "podlaz0", CIDR: planner.DefaultTunIPv4CIDR}, liveTunLinkProofForTest())
	if err == nil || !errors.Is(err, ErrTunLinkIdentityMismatch) {
		t.Fatalf("expected replacement-race failure, got %v", err)
	}
	runner.assertDone()
}

func TestIPTunAddressApplyUsesExactArgvAndReturnsOwnership(t *testing.T) {
	plan := boundAddressPlanForTest()
	runner := &scriptedRunner{t: t, steps: []scriptedCommand{
		linkCommandResult(7, "", nil),
		addressCommandResult(""),
		linkCommandResult(7, "", nil),
		linkCommandResult(7, "", nil),
		commandResult([]string{"ip", "-4", "address", "replace", planner.DefaultTunIPv4CIDR, "dev", "podlaz0"}, CommandResult{}, nil),
		linkCommandResult(7, "", nil),
		addressCommandResult(addressLineForTest(7, planner.DefaultTunIPv4CIDR)),
		linkCommandResult(7, "", nil),
		linkCommandResult(7, "", nil),
		commandResult([]string{"ip", "link", "set", "dev", "podlaz0", "up"}, CommandResult{}, nil),
		linkCommandResult(7, "", nil),
		addressCommandResult(addressLineForTest(7, planner.DefaultTunIPv4CIDR)),
		linkCommandResult(7, "", nil),
		linkCommandResult(7, "", nil),
	}}

	step, err := (IPTunAddressExecutor{Runner: runner}).Apply(context.Background(), plan)
	if err != nil {
		t.Fatalf("apply TUN address: %v", err)
	}
	if step.Kind != "tun-address" || step.Owner != OwnerTunAddress || !strings.Contains(step.Target, "7") || !strings.Contains(step.Target, planner.DefaultTunIPv4CIDR) {
		t.Fatalf("unexpected address ownership step: %#v", step)
	}
	runner.assertDone()
}

func TestIPTunAddressApplyRefusesPreExistingExactAddressWithoutOwnedRetry(t *testing.T) {
	plan := boundAddressPlanForTest()
	runner := &scriptedRunner{t: t, steps: []scriptedCommand{
		linkCommandResult(7, "", nil),
		addressCommandResult(addressLineForTest(7, planner.DefaultTunIPv4CIDR)),
		linkCommandResult(7, "", nil),
	}}

	step, err := (IPTunAddressExecutor{Runner: runner}).Apply(context.Background(), plan)
	if err == nil || !errors.Is(err, ErrTunAddressConflict) {
		t.Fatalf("expected fail-closed exact address conflict, got step=%#v err=%v", step, err)
	}
	if step.Kind != "" {
		t.Fatalf("foreign address must not produce owned step: %#v", step)
	}
	runner.assertDone()
}

func TestIPTunAddressApplyReturnsPartialOwnershipAfterReplace(t *testing.T) {
	plan := boundAddressPlanForTest()
	upErr := errors.New("link set failed")
	runner := &scriptedRunner{t: t, steps: []scriptedCommand{
		linkCommandResult(7, "", nil),
		addressCommandResult(""),
		linkCommandResult(7, "", nil),
		linkCommandResult(7, "", nil),
		commandResult([]string{"ip", "-4", "address", "replace", planner.DefaultTunIPv4CIDR, "dev", "podlaz0"}, CommandResult{}, nil),
		linkCommandResult(7, "", nil),
		addressCommandResult(addressLineForTest(7, planner.DefaultTunIPv4CIDR)),
		linkCommandResult(7, "", nil),
		linkCommandResult(7, "", nil),
		commandResult([]string{"ip", "link", "set", "dev", "podlaz0", "up"}, CommandResult{ExitCode: 2, Stderr: upErr.Error()}, upErr),
	}}

	step, err := (IPTunAddressExecutor{Runner: runner}).Apply(context.Background(), plan)
	if err == nil || !errors.Is(err, ErrTunAddressApply) || step.Kind != "tun-address" || step.Owner != OwnerTunAddress {
		t.Fatalf("expected rollbackable partial address ownership, got step=%#v err=%v", step, err)
	}
	runner.assertDone()
}

func TestIPTunAddressApplyReturnsOwnershipWhenReplaceCompletionIsAmbiguous(t *testing.T) {
	plan := boundAddressPlanForTest()
	timeoutErr := context.DeadlineExceeded
	runner := &scriptedRunner{t: t, steps: []scriptedCommand{
		linkCommandResult(7, "", nil),
		addressCommandResult(""),
		linkCommandResult(7, "", nil),
		linkCommandResult(7, "", nil),
		commandResult([]string{"ip", "-4", "address", "replace", planner.DefaultTunIPv4CIDR, "dev", "podlaz0"}, CommandResult{ExitCode: -1}, timeoutErr),
	}}

	step, err := (IPTunAddressExecutor{Runner: runner}).Apply(context.Background(), plan)
	if err == nil || !errors.Is(err, ErrTunAddressApply) || !errors.Is(err, timeoutErr) {
		t.Fatalf("expected ambiguous replace failure, got step=%#v err=%v", step, err)
	}
	if step.Kind != "tun-address" || step.Owner != OwnerTunAddress || !strings.Contains(step.Target, "ifindex=7") {
		t.Fatalf("mutable address command must retain rollback ownership, got %#v", step)
	}
	runner.assertDone()
}

func TestIPTunAddressVerifyRejectsAdditionalForeignIPv4Address(t *testing.T) {
	plan := boundAddressPlanForTest()
	inventory := addressLineForTest(7, planner.DefaultTunIPv4CIDR) + "\n" + addressLineForTest(7, "198.51.100.25/32")
	runner := &scriptedRunner{t: t, steps: []scriptedCommand{
		linkCommandResult(7, "", nil),
		addressCommandResult(inventory),
		linkCommandResult(7, "", nil),
	}}

	err := (IPTunAddressExecutor{Runner: runner}).Verify(context.Background(), plan)
	if err == nil || !errors.Is(err, ErrTunAddressVerify) || !strings.Contains(err.Error(), "conflicting IPv4") {
		t.Fatalf("expected foreign IPv4 verification failure, got %v", err)
	}
	runner.assertDone()
}

func TestIPTunAddressVerifyRequiresExactSingleAddressAndUpIdentity(t *testing.T) {
	plan := boundAddressPlanForTest()
	runner := &scriptedRunner{t: t, steps: []scriptedCommand{
		linkCommandResult(7, "", nil),
		addressCommandResult(addressLineForTest(7, planner.DefaultTunIPv4CIDR)),
		linkCommandResult(7, "", nil),
		linkCommandResult(7, "", nil),
	}}
	if err := (IPTunAddressExecutor{Runner: runner}).Verify(context.Background(), plan); err != nil {
		t.Fatalf("verify exact TUN address: %v", err)
	}
	runner.assertDone()
}

func TestIPTunAddressVerifyRejectsDuplicateAddress(t *testing.T) {
	plan := boundAddressPlanForTest()
	line := addressLineForTest(7, planner.DefaultTunIPv4CIDR)
	runner := &scriptedRunner{t: t, steps: []scriptedCommand{
		linkCommandResult(7, "", nil),
		addressCommandResult(line + "\n" + line),
		linkCommandResult(7, "", nil),
	}}
	if err := (IPTunAddressExecutor{Runner: runner}).Verify(context.Background(), plan); err == nil || !errors.Is(err, ErrTunAddressVerify) || !strings.Contains(err.Error(), "exactly once") {
		t.Fatalf("expected duplicate address verification failure, got %v", err)
	}
	runner.assertDone()
}

func TestIPTunAddressRollbackDeletesOnlyExactAddressFromBoundLink(t *testing.T) {
	plan := boundAddressPlanForTest()
	runner := &scriptedRunner{t: t, steps: []scriptedCommand{
		linkCommandResult(7, "", nil),
		addressCommandResult(addressLineForTest(7, planner.DefaultTunIPv4CIDR)),
		linkCommandResult(7, "", nil),
		linkCommandResult(7, "", nil),
		commandResult([]string{"ip", "-4", "address", "del", planner.DefaultTunIPv4CIDR, "dev", "podlaz0"}, CommandResult{}, nil),
		linkCommandResult(7, "", nil),
		addressCommandResult(""),
		linkCommandResult(7, "", nil),
	}}
	if err := (IPTunAddressExecutor{Runner: runner}).Rollback(context.Background(), plan); err != nil {
		t.Fatalf("rollback exact TUN address: %v", err)
	}
	runner.assertDone()
}

func TestIPTunAddressRollbackRefusesReplacementLink(t *testing.T) {
	plan := boundAddressPlanForTest()
	runner := &scriptedRunner{t: t, steps: []scriptedCommand{
		linkCommandResult(8, "", nil),
	}}
	if err := (IPTunAddressExecutor{Runner: runner}).Rollback(context.Background(), plan); err == nil || !errors.Is(err, ErrTunLinkIdentityMismatch) {
		t.Fatalf("expected replacement-link refusal, got %v", err)
	}
	runner.assertDone()
}

func commandResult(want []string, result CommandResult, err error) scriptedCommand {
	return scriptedCommand{want: want, result: result, err: err}
}

func linkCommandResult(index int, stderr string, err error) scriptedCommand {
	result := CommandResult{Stdout: tunLinkDetailsForAddressTest(index, true)}
	if err != nil {
		result = CommandResult{ExitCode: 1, Stderr: stderr}
	}
	return commandResult([]string{"ip", "-details", "-o", "link", "show", "dev", "podlaz0"}, result, err)
}

func addressCommandResult(stdout string) scriptedCommand {
	return commandResult([]string{"ip", "-4", "-o", "address", "show", "dev", "podlaz0"}, CommandResult{Stdout: stdout}, nil)
}

func boundAddressPlanForTest() planner.TunAddressPlan {
	return planner.TunAddressPlan{
		Family:            "ipv4",
		Interface:         "podlaz0",
		CIDR:              planner.DefaultTunIPv4CIDR,
		Scope:             "global",
		Action:            planner.TunAddressActionAssign,
		Owner:             planner.TunAddressOwner,
		RollbackKey:       "podlaz0/" + planner.DefaultTunIPv4CIDR,
		LinkIndex:         7,
		LinkKind:          "tun",
		AppearedAfterCore: true,
	}
}

func tunLinkDetailsForAddressTest(index int, up bool) string {
	flags := "POINTOPOINT,NOARP,MULTICAST"
	state := "DOWN"
	if up {
		flags = "POINTOPOINT,NOARP,UP,LOWER_UP"
		state = "UP"
	}
	return strings.Join([]string{
		strconv.Itoa(index) + ": podlaz0: <" + flags + "> mtu 1500 qdisc fq_codel state " + state + " mode DEFAULT group default qlen 500",
		"    link/none promiscuity 0 allmulti 0 minmtu 68 maxmtu 65535",
		"    tun type tun pi off vnet_hdr on persist off",
	}, "\n")
}

func addressLineForTest(index int, cidr string) string {
	return strconv.Itoa(index) + ": podlaz0    inet " + cidr + " scope global podlaz0\\       valid_lft forever preferred_lft forever"
}

func liveTunLinkProofForTest() TunLinkCreationProof {
	return TunLinkCreationProof{PreStartAbsent: true, TrackedCorePID: 1234, CoreDone: make(chan struct{})}
}
