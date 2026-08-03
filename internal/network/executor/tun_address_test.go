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
		{want: []string{"ip", "-details", "-o", "link", "show", "dev", "podlaz0"}, result: CommandResult{ExitCode: 1, Stderr: `Device "podlaz0" does not exist.`}, err: missing},
		{want: []string{"ip", "-details", "-o", "link", "show", "dev", "podlaz0"}, result: CommandResult{Stdout: tunLinkDetailsForAddressTest(7, true)}},
	}}
	exec := IPTunAddressExecutor{Runner: runner, BindAttempts: 2, BindPollInterval: time.Nanosecond, Sleep: func(context.Context, time.Duration) error { return nil }}

	bound, err := exec.Bind(context.Background(), planner.TunAddressPlan{Interface: "podlaz0", CIDR: planner.DefaultTunIPv4CIDR, LinkKind: "tun"})
	if err != nil {
		t.Fatalf("bind TUN address identity: %v", err)
	}
	if bound.LinkIndex != 7 || bound.LinkKind != "tun" || !bound.AppearedAfterCore {
		t.Fatalf("unexpected bound identity: %#v", bound)
	}
	runner.assertDone()
}

func TestIPTunAddressApplyUsesExactArgvAndReturnsOwnership(t *testing.T) {
	plan := boundAddressPlanForTest()
	runner := &scriptedRunner{t: t, steps: []scriptedCommand{
		{want: []string{"ip", "-details", "-o", "link", "show", "dev", "podlaz0"}, result: CommandResult{Stdout: tunLinkDetailsForAddressTest(7, true)}},
		{want: []string{"ip", "-4", "-o", "address", "show", "dev", "podlaz0"}},
		{want: []string{"ip", "-4", "address", "replace", planner.DefaultTunIPv4CIDR, "dev", "podlaz0"}},
		{want: []string{"ip", "link", "set", "dev", "podlaz0", "up"}},
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
		{want: []string{"ip", "-details", "-o", "link", "show", "dev", "podlaz0"}, result: CommandResult{Stdout: tunLinkDetailsForAddressTest(7, true)}},
		{want: []string{"ip", "-4", "-o", "address", "show", "dev", "podlaz0"}, result: CommandResult{Stdout: addressLineForTest(7, planner.DefaultTunIPv4CIDR)}},
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
		{want: []string{"ip", "-details", "-o", "link", "show", "dev", "podlaz0"}, result: CommandResult{Stdout: tunLinkDetailsForAddressTest(7, true)}},
		{want: []string{"ip", "-4", "-o", "address", "show", "dev", "podlaz0"}},
		{want: []string{"ip", "-4", "address", "replace", planner.DefaultTunIPv4CIDR, "dev", "podlaz0"}},
		{want: []string{"ip", "link", "set", "dev", "podlaz0", "up"}, result: CommandResult{ExitCode: 2, Stderr: upErr.Error()}, err: upErr},
	}}

	step, err := (IPTunAddressExecutor{Runner: runner}).Apply(context.Background(), plan)
	if err == nil || !errors.Is(err, ErrTunAddressApply) || step.Kind != "tun-address" || step.Owner != OwnerTunAddress {
		t.Fatalf("expected rollbackable partial address ownership, got step=%#v err=%v", step, err)
	}
	runner.assertDone()
}

func TestIPTunAddressVerifyRequiresExactSingleAddressAndUpIdentity(t *testing.T) {
	plan := boundAddressPlanForTest()
	runner := &scriptedRunner{t: t, steps: []scriptedCommand{
		{want: []string{"ip", "-details", "-o", "link", "show", "dev", "podlaz0"}, result: CommandResult{Stdout: tunLinkDetailsForAddressTest(7, true)}},
		{want: []string{"ip", "-4", "-o", "address", "show", "dev", "podlaz0"}, result: CommandResult{Stdout: addressLineForTest(7, planner.DefaultTunIPv4CIDR)}},
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
		{want: []string{"ip", "-details", "-o", "link", "show", "dev", "podlaz0"}, result: CommandResult{Stdout: tunLinkDetailsForAddressTest(7, true)}},
		{want: []string{"ip", "-4", "-o", "address", "show", "dev", "podlaz0"}, result: CommandResult{Stdout: line + "\n" + line}},
	}}
	if err := (IPTunAddressExecutor{Runner: runner}).Verify(context.Background(), plan); err == nil || !errors.Is(err, ErrTunAddressVerify) || !strings.Contains(err.Error(), "exactly once") {
		t.Fatalf("expected duplicate address verification failure, got %v", err)
	}
	runner.assertDone()
}

func TestIPTunAddressRollbackDeletesOnlyExactAddressFromBoundLink(t *testing.T) {
	plan := boundAddressPlanForTest()
	runner := &scriptedRunner{t: t, steps: []scriptedCommand{
		{want: []string{"ip", "-details", "-o", "link", "show", "dev", "podlaz0"}, result: CommandResult{Stdout: tunLinkDetailsForAddressTest(7, true)}},
		{want: []string{"ip", "-4", "-o", "address", "show", "dev", "podlaz0"}, result: CommandResult{Stdout: addressLineForTest(7, planner.DefaultTunIPv4CIDR)}},
		{want: []string{"ip", "-4", "address", "del", planner.DefaultTunIPv4CIDR, "dev", "podlaz0"}},
	}}
	if err := (IPTunAddressExecutor{Runner: runner}).Rollback(context.Background(), plan); err != nil {
		t.Fatalf("rollback exact TUN address: %v", err)
	}
	runner.assertDone()
}

func TestIPTunAddressRollbackRefusesReplacementLink(t *testing.T) {
	plan := boundAddressPlanForTest()
	runner := &scriptedRunner{t: t, steps: []scriptedCommand{
		{want: []string{"ip", "-details", "-o", "link", "show", "dev", "podlaz0"}, result: CommandResult{Stdout: tunLinkDetailsForAddressTest(8, true)}},
	}}
	if err := (IPTunAddressExecutor{Runner: runner}).Rollback(context.Background(), plan); err == nil || !errors.Is(err, ErrTunLinkIdentityMismatch) {
		t.Fatalf("expected replacement-link refusal, got %v", err)
	}
	runner.assertDone()
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
