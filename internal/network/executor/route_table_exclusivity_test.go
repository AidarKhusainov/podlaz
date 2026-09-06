package executor

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func TestExclusiveAllocatedRouteTableDetectsForeignRaceAndRollsBackOnlyOwnedRoute(t *testing.T) {
	runner := &allocatedRouteTableRaceRunner{}
	exec := IPRouteExecutor{
		Runner: runner,
		AllocationEvidenceCollector: func(context.Context) (netsnapshot.TunAllocationEvidence, error) {
			return runner.allocationEvidence(), nil
		},
	}
	plan := allocatedRouteTablePlan()

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

func TestExclusiveAllocatedRouteVerificationRejectsNonDefaultRoute(t *testing.T) {
	exec := IPRouteExecutor{Runner: nonDefaultAllocatedRouteRunner{}}
	plan := allocatedRouteTablePlan()

	if err := exec.Verify(context.Background(), plan); err == nil {
		t.Fatal("non-default route must not satisfy exclusive default-route verification")
	}
}

func TestExclusiveAllocatedRouteTableRejectsFreshKernelReservationBeforeMutation(t *testing.T) {
	tests := []struct {
		name     string
		evidence netsnapshot.TunAllocationEvidence
	}{
		{
			name: "vrf reservation",
			evidence: netsnapshot.TunAllocationEvidence{
				ReservedRoutingTables: []uint32{51821},
			},
		},
		{
			name: "foreign policy rule",
			evidence: netsnapshot.TunAllocationEvidence{
				IPv4PolicyRules: []netsnapshot.TunAllocationRule{{Priority: 12000, Table: 51821}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &allocatedRouteTableAuthorityRunner{}
			exec := IPRouteExecutor{
				Runner: runner,
				AllocationEvidenceCollector: func(context.Context) (netsnapshot.TunAllocationEvidence, error) {
					return tt.evidence, nil
				},
			}

			if step, err := exec.Add(context.Background(), allocatedRouteTablePlan()); err == nil {
				t.Fatalf("fresh foreign reservation must block before mutation, got step=%#v", step)
			}
			if runner.addCalls != 0 || runner.ownDefault {
				t.Fatalf("foreign reservation allowed route mutation: adds=%d own=%v", runner.addCalls, runner.ownDefault)
			}
		})
	}
}

func TestExclusiveAllocatedRouteTableDetectsFreshReservationAfterMutation(t *testing.T) {
	runner := &allocatedRouteTableAuthorityRunner{}
	calls := 0
	exec := IPRouteExecutor{
		Runner: runner,
		AllocationEvidenceCollector: func(context.Context) (netsnapshot.TunAllocationEvidence, error) {
			calls++
			if calls == 1 {
				return netsnapshot.TunAllocationEvidence{}, nil
			}
			return netsnapshot.TunAllocationEvidence{
				IPv4Routes:            []netsnapshot.TunAllocationRoute{{Default: true, Table: 51821}},
				ReservedRoutingTables: []uint32{51821},
			}, nil
		},
	}
	plan := allocatedRouteTablePlan()

	step, err := exec.Add(context.Background(), plan)
	if err == nil {
		t.Fatalf("post-mutation reservation race must fail, got step=%#v", step)
	}
	if step.Kind != "route" || step.Owner != OwnerRoute {
		t.Fatalf("post-mutation reservation race must retain exact rollback step, got %#v err=%v", step, err)
	}
	if !runner.ownDefault {
		t.Fatal("race fixture did not apply the owned route before fresh reservation appeared")
	}

	if err := exec.Rollback(context.Background(), plan); err != nil {
		t.Fatalf("rollback exact owned route after reservation race: %v", err)
	}
	if runner.ownDefault {
		t.Fatal("rollback left the Podlaz-owned route after reservation race")
	}
}

func allocatedRouteTablePlan() planner.TunRoutePlan {
	return planner.TunRoutePlan{
		Family:      "ipv4",
		Destination: planner.IPv4DefaultRoute,
		Table:       "51821",
		Interface:   "podlaz0",
		Action:      planner.TunActionAddExclusive,
		Reason:      "synthetic allocated session route",
	}
}

type allocatedRouteTableRaceRunner struct {
	ownDefault   bool
	foreignRoute bool
}

func (r *allocatedRouteTableRaceRunner) allocationEvidence() netsnapshot.TunAllocationEvidence {
	var routes []netsnapshot.TunAllocationRoute
	if r.ownDefault {
		routes = append(routes, netsnapshot.TunAllocationRoute{Default: true, Table: 51821})
	}
	if r.foreignRoute {
		routes = append(routes, netsnapshot.TunAllocationRoute{Table: 51821})
	}
	return netsnapshot.TunAllocationEvidence{IPv4Routes: routes}
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
		// Foreign state appears after the empty-bucket check and before the
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

type allocatedRouteTableAuthorityRunner struct {
	ownDefault bool
	addCalls   int
}

func (r *allocatedRouteTableAuthorityRunner) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	command := strings.TrimSpace(name + " " + strings.Join(args, " "))
	switch command {
	case "ip -N -4 -o route show table 51821":
		if r.ownDefault {
			return CommandResult{Stdout: "default dev podlaz0 table 51821"}, nil
		}
		return CommandResult{}, nil
	case "ip -4 route add default dev podlaz0 table 51821":
		r.addCalls++
		r.ownDefault = true
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

type nonDefaultAllocatedRouteRunner struct{}

func (nonDefaultAllocatedRouteRunner) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	command := strings.TrimSpace(name + " " + strings.Join(args, " "))
	if command == "ip -N -4 -o route show table 51821" {
		return CommandResult{Stdout: "198.51.100.0/24 dev podlaz0 table 51821"}, nil
	}
	return CommandResult{ExitCode: 127, Stderr: "unexpected command"}, fmt.Errorf("unexpected command: %s", command)
}
