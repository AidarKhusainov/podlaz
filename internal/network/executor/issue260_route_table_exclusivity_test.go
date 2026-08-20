package executor

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestExclusiveAllocatedRouteTableDetectsForeignRaceAndRollsBackOnlyOwnedRoute(t *testing.T) {
	runner := &allocatedRouteTableRaceRunner{}
	exec := IPRouteExecutor{Runner: runner}
	plan := planner.TunRoutePlan{
		Family:      "ipv4",
		Destination: planner.IPv4DefaultRoute,
		Table:       "51821",
		Interface:   "podlaz0",
		Action:      planner.TunActionAddExclusive,
		Reason:      "synthetic allocated session route",
	}

	step, err := exec.Add(context.Background(), plan)
	if err == nil {
		t.Fatalf("expected post-add allocated-table collision, got step=%#v", step)
	}
	if step.Kind != "route" || step.Owner != OwnerRoute {
		t.Fatalf("post-mutation table collision must retain exact owned rollback step, got %#v err=%v", step, err)
	}
	if !runner.ownDefault || !runner.foreignRoute {
		t.Fatalf("race fixture did not create both routes: own=%v foreign=%v", runner.ownDefault, runner.foreignRoute)
	}

	if err := exec.Rollback(context.Background(), plan); err != nil {
		t.Fatalf("rollback exact owned default route: %v", err)
	}
	if runner.ownDefault {
		t.Fatal("rollback left the Podlaz-owned default route behind")
	}
	if !runner.foreignRoute {
		t.Fatal("rollback removed the foreign route from the allocated table")
	}
}

type allocatedRouteTableRaceRunner struct {
	ownDefault   bool
	foreignRoute bool
}

func (r *allocatedRouteTableRaceRunner) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	command := strings.TrimSpace(name + " " + strings.Join(args, " "))
	switch command {
	case "ip -N -4 -o route show table 51821":
		var lines []string
		if r.ownDefault {
			lines = append(lines, "default dev podlaz0 table 51821")
		}
		if r.foreignRoute {
			lines = append(lines, "198.51.100.0/24 dev eth9 table 51821")
		}
		return CommandResult{Stdout: strings.Join(lines, "\n")}, nil
	case "ip -4 route add default dev podlaz0 table 51821":
		r.ownDefault = true
		// Foreign state appears after the live empty-table check but before the
		// session can prove that the table remains exclusive.
		r.foreignRoute = true
		return CommandResult{}, nil
	case "ip -4 route del default dev podlaz0 table 51821":
		r.ownDefault = false
		return CommandResult{}, nil
	case "ip -4 route flush cache":
		return CommandResult{}, nil
	default:
		return CommandResult{ExitCode: 127, Stderr: "unexpected command"}, fmt.Errorf("unexpected command: %s", command)
	}
}
