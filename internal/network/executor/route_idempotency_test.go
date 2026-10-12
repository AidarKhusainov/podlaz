package executor

import (
	"context"
	"reflect"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestIPRouteAddSkipsMatchingExistingMainServerBypassRoute(t *testing.T) {
	runner := &recordingRunner{stdout: "203.0.113.10 via 192.0.2.1 dev eth0 src 192.0.2.20"}
	route := planner.TunRoutePlan{Destination: "203.0.113.10/32", Table: planner.MainRoutingTable, Interface: "eth0", Gateway: "192.0.2.1", Action: "add"}

	step, err := (IPRouteExecutor{Runner: runner}).Add(context.Background(), route)
	if err != nil {
		t.Fatalf("expected matching existing route to be accepted: %v", err)
	}
	if step.Kind != "" {
		t.Fatalf("expected no applied step for pre-existing route, got %#v", step)
	}
	want := [][]string{{"ip", "-4", "route", "show", "table", "main", "203.0.113.10/32"}}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("unexpected commands:\nwant %#v\n got %#v", want, runner.commands)
	}
}

func TestIPRouteAddAddsMissingMainServerBypassRoute(t *testing.T) {
	runner := &recordingRunner{results: []CommandResult{{Stdout: ""}, {}, {}}}
	route := planner.TunRoutePlan{Destination: "203.0.113.10/32", Table: planner.MainRoutingTable, Interface: "eth0", Gateway: "192.0.2.1", Action: "add"}

	step, err := (IPRouteExecutor{Runner: runner}).Add(context.Background(), route)
	if err != nil {
		t.Fatalf("expected missing route to be added: %v", err)
	}
	if step.Kind != "route" || step.Owner != OwnerRoute {
		t.Fatalf("expected applied route step, got %#v", step)
	}
	want := [][]string{
		{"ip", "-4", "route", "show", "table", "main", "203.0.113.10/32"},
		{"ip", "-4", "route", "add", "203.0.113.10/32", "via", "192.0.2.1", "dev", "eth0", "table", "main"},
		{"ip", "-4", "route", "flush", "cache"},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("unexpected commands:\nwant %#v\n got %#v", want, runner.commands)
	}
}

func TestIPRouteAddFailsForConflictingExistingRoute(t *testing.T) {
	runner := &recordingRunner{stdout: "203.0.113.10 via 192.0.2.254 dev wlan0"}
	route := planner.TunRoutePlan{Destination: "203.0.113.10/32", Table: planner.MainRoutingTable, Interface: "eth0", Gateway: "192.0.2.1", Action: "add"}
	if _, err := (IPRouteExecutor{Runner: runner}).Add(context.Background(), route); err == nil {
		t.Fatal("expected conflicting existing route to fail")
	}
	want := [][]string{{"ip", "-4", "route", "show", "table", "main", "203.0.113.10/32"}}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("unexpected commands:\nwant %#v\n got %#v", want, runner.commands)
	}
}

func TestTunRouteScopeIsAppliedAndVerifiedWithoutWeakeningLegacyRoutes(t *testing.T) {
	newRoute := planner.TunRoutePlan{Destination: "default", Interface: "podlaz0", Table: "51821", Scope: "global"}
	wantAdd := []string{"-4", "route", "add", "default", "dev", "podlaz0", "table", "51821", "scope", "global"}
	if got := routeArgs("add", newRoute); !reflect.DeepEqual(got, wantAdd) {
		t.Fatalf("new TUN route add: got %#v, want %#v", got, wantAdd)
	}
	wantDelete := []string{"-4", "route", "del", "default", "dev", "podlaz0", "table", "51821"}
	if got := routeArgs("del", newRoute); !reflect.DeepEqual(got, wantDelete) {
		t.Fatalf("scope must not be a deletion authority: got %#v, want %#v", got, wantDelete)
	}
	for _, line := range []string{
		"default dev podlaz0",
		"default dev podlaz0 scope global",
		"default dev podlaz0 scope 0",
	} {
		if err := verifyRouteLine(line, newRoute); err != nil {
			t.Errorf("global TUN route %q rejected: %v", line, err)
		}
	}
	for _, line := range []string{
		"default dev podlaz0 scope link",
		"default dev podlaz0 scope 253",
	} {
		if err := verifyRouteLine(line, newRoute); err == nil {
			t.Errorf("link-scoped TUN route %q incorrectly verified as global", line)
		}
	}
	legacy := newRoute
	legacy.Scope = ""
	if err := verifyRouteLine("default dev podlaz0 scope link", legacy); err != nil {
		t.Fatalf("legacy persisted route should stay verifiable without migration: %v", err)
	}
}
