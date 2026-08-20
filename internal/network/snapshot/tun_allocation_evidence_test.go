package snapshot

import (
	"context"
	"testing"
)

func TestEnsureTunAllocationEvidenceUsesNumericIPOutput(t *testing.T) {
	runner := fakeRunner{
		paths: map[string]string{"ip": "/usr/sbin/ip"},
		commands: map[string]CommandResult{
			"/usr/sbin/ip -N -4 -o route show table all": {
				Stdout: "default dev test0 table 51820\n198.51.100.0/24 dev eth0 table 60000",
			},
			"/usr/sbin/ip -N -4 rule show": {
				Stdout: "0: from all lookup 255\n9999: from all to 203.0.113.10 lookup 254\n10000: from all lookup 51820\n32766: from all lookup 254\n32767: from all lookup 253",
			},
		},
	}

	s := ensureTunAllocationEvidenceWithRunner(context.Background(), runner, Snapshot{OS: "linux"})
	if s.IPv4Routes.Inspection.Status != StatusDetected || len(s.IPv4Routes.Routes) != 2 {
		t.Fatalf("unexpected numeric route inventory: %#v", s.IPv4Routes)
	}
	if s.IPv4Routes.Routes[0].Table != "51820" || s.IPv4Routes.Routes[1].Table != "60000" {
		t.Fatalf("route table identities are not numeric: %#v", s.IPv4Routes.Routes)
	}
	if s.IPv4PolicyRules.Inspection.Status != StatusDetected || len(s.IPv4PolicyRules.Rules) != 2 {
		t.Fatalf("unexpected numeric policy-rule inventory: %#v", s.IPv4PolicyRules)
	}
	if s.IPv4PolicyRules.Rules[0].Table != "254" || s.IPv4PolicyRules.Rules[1].Table != "51820" {
		t.Fatalf("policy-rule table identities are not numeric: %#v", s.IPv4PolicyRules.Rules)
	}
}

func TestEnsureTunAllocationEvidenceFailsClosedWhenNumericInventoryFails(t *testing.T) {
	runner := fakeRunner{
		paths: map[string]string{"ip": "/usr/sbin/ip"},
		commands: map[string]CommandResult{
			"/usr/sbin/ip -N -4 -o route show table all": {ExitCode: 1, Stderr: "synthetic route inspection failure"},
			"/usr/sbin/ip -N -4 rule show":               {ExitCode: 1, Stderr: "synthetic rule inspection failure"},
		},
	}

	s := ensureTunAllocationEvidenceWithRunner(context.Background(), runner, Snapshot{OS: "linux"})
	if s.IPv4Routes.Inspection.Status != StatusUnknown || s.IPv4PolicyRules.Inspection.Status != StatusUnknown {
		t.Fatalf("failed numeric inventories must remain fail-closed: routes=%#v rules=%#v", s.IPv4Routes, s.IPv4PolicyRules)
	}
}
