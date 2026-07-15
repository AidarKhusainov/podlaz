package daemon

import (
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

func TestParseTunDiagnosticRouteAndPolicyRule(t *testing.T) {
	route := parseTunDiagnosticRoute("1.1.1.1 dev podlaz0 table 51820 src 10.0.0.2\n")
	if route.Interface != "podlaz0" || route.Table != "51820" {
		t.Fatalf("unexpected route evidence: %#v", route)
	}
	if !tunDiagnosticHasPolicyRule("10000: from all lookup 51820\n", planner.TunRulePriority) {
		t.Fatal("expected podlaz policy rule to be detected")
	}
}

func TestProbeTunDNSStateRejectsForeignRouteOnlyOwner(t *testing.T) {
	result := probeTunDNSState(planner.TunPlan{DNS: planner.TunDNSPlan{TargetLink: "podlaz0", Servers: []string{"1.1.1.1"}}}, netsnapshot.Snapshot{
		DNS: netsnapshot.DNSState{ResolvedLinks: []netsnapshot.ResolvedLink{
			{Name: "podlaz0", DNSServers: []string{"1.1.1.1"}, DNSDomains: []string{"~."}, Protocols: []string{"+DefaultRoute"}},
			{Name: "wg0", DNSDomains: []string{"~."}},
		}},
	})
	if result.Status != tundiag.ProbeFail || result.Classification != tundiag.ClassForeignDNSConflict {
		t.Fatalf("unexpected DNS state result: %#v", result)
	}
}

func TestTunDiagnosticCappedBufferDoesNotGrowPastLimit(t *testing.T) {
	buffer := newTunDiagnosticCappedBuffer(8)
	input := strings.Repeat("x", 64)
	count, err := buffer.Write([]byte(input))
	if err != nil || count != len(input) {
		t.Fatalf("unexpected write result count=%d err=%v", count, err)
	}
	if len(buffer.String()) != 8 {
		t.Fatalf("expected 8 stored bytes, got %d", len(buffer.String()))
	}
}
