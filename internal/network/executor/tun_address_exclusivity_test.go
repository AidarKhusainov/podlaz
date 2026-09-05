package executor

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"golang.org/x/sys/unix"
)

func TestExclusiveTunAddressDetectsForeignRaceAndRollsBackOnlyOwnedAddress(t *testing.T) {
	runner := &tunAddressAllocationRaceRunner{}
	exec := IPTunAddressExecutor{
		Runner: runner,
		AllocationEvidenceCollector: func(context.Context) (netsnapshot.TunAllocationEvidence, error) {
			evidence := netsnapshot.TunAllocationEvidence{}
			if runner.ownAddress {
				evidence.IPv4Addresses = append(evidence.IPv4Addresses, netip.MustParsePrefix(planner.DefaultTunIPv4CIDR))
				evidence.IPv4Routes = append(evidence.IPv4Routes, netsnapshot.TunAllocationRoute{
					Destination: netip.MustParsePrefix(planner.DefaultTunIPv4CIDR),
					Table:       unix.RT_TABLE_LOCAL,
					Type:        unix.RTN_LOCAL,
					LinkIndex:   7,
				})
			}
			if runner.foreignAddress {
				evidence.IPv4Addresses = append(evidence.IPv4Addresses, netip.MustParsePrefix(planner.DefaultTunIPv4CIDR))
			}
			return evidence, nil
		},
	}
	plan := rollbackIdentityAddressPlanForTest()
	plan.Action = planner.TunAddressActionAssignExclusive

	step, err := exec.Apply(context.Background(), plan)
	if err == nil || !errors.Is(err, ErrTunAddressConflict) {
		t.Fatalf("expected apply-time global address collision, got step=%#v err=%v", step, err)
	}
	if step.Kind != "tun-address" || step.Owner != OwnerTunAddress {
		t.Fatalf("post-mutation collision must retain exact owned rollback step, got %#v", step)
	}
	if !runner.ownAddress || !runner.foreignAddress {
		t.Fatalf("race fixture did not create both address copies: own=%v foreign=%v", runner.ownAddress, runner.foreignAddress)
	}

	if err := exec.Rollback(context.Background(), plan); err != nil {
		t.Fatalf("rollback exact owned address after race: %v", err)
	}
	if runner.ownAddress {
		t.Fatal("rollback left the Podlaz-owned address behind")
	}
	if !runner.foreignAddress {
		t.Fatal("rollback removed the foreign raced address")
	}
}

type tunAddressAllocationRaceRunner struct {
	ownAddress     bool
	foreignAddress bool
	linkUp         bool
	commands       []string
}

func (r *tunAddressAllocationRaceRunner) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	command := strings.TrimSpace(name + " " + strings.Join(args, " "))
	r.commands = append(r.commands, command)

	switch command {
	case "ip -details -o link show dev podlaz0":
		flags := "<POINTOPOINT,NOARP>"
		state := "DOWN"
		if r.linkUp {
			flags = "<POINTOPOINT,NOARP,UP,LOWER_UP>"
			state = "UNKNOWN"
		}
		return CommandResult{Stdout: fmt.Sprintf("7: podlaz0: %s mtu 1500 state %s mode DEFAULT group default qlen 500 tun type tun pi off", flags, state)}, nil
	case "ip -4 -o address show dev podlaz0":
		if r.ownAddress {
			return CommandResult{Stdout: "7: podlaz0 inet " + planner.DefaultTunIPv4CIDR + " scope global podlaz0"}, nil
		}
		return CommandResult{}, nil
	case "ip -4 address replace " + planner.DefaultTunIPv4CIDR + " dev podlaz0":
		r.ownAddress = true
		// Simulate a foreign actor winning the TOCTOU window after the live
		// pre-fence but before Podlaz can prove the post-mutation invariant.
		r.foreignAddress = true
		return CommandResult{}, nil
	case "ip link set dev podlaz0 up":
		r.linkUp = true
		return CommandResult{}, nil
	case "ip -4 address del " + planner.DefaultTunIPv4CIDR + " dev podlaz0":
		r.ownAddress = false
		return CommandResult{}, nil
	default:
		return CommandResult{ExitCode: 127, Stderr: "unexpected command"}, fmt.Errorf("unexpected command: %s", command)
	}
}
