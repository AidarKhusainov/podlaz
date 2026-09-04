package cli

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"

	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func TestRunCLIPlanTunFailsClosedWhenTypedAllocationAuthorityIsUnavailable(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := planTestOptions(t, storePath, netsnapshot.FakeResolvedDesktop())
	calls := 0
	opts.tunAllocationEvidence = func(context.Context) (netsnapshot.TunAllocationEvidence, error) {
		calls++
		return netsnapshot.TunAllocationEvidence{}, errors.New("synthetic rtnetlink failure")
	}
	profileID := importPlanTestProfile(t, opts)

	err := runWithOptions(context.Background(), []string{"plan", "--mode", "tun", profileID}, &bytes.Buffer{}, opts)
	if err == nil {
		t.Fatal("expected unavailable typed allocation authority to block TUN planning")
	}
	if calls != 1 {
		t.Fatalf("allocation authority collector calls = %d, want 1", calls)
	}
	if !strings.Contains(err.Error(), "synthetic rtnetlink failure") {
		t.Fatalf("unexpected allocation authority error: %v", err)
	}
}

func TestRunCLIPlanTunSeparatesDiagnosticsFromTypedAllocationAuthority(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	diagnostic := netsnapshot.FakeResolvedDesktop()
	diagnostic.IPv4Addresses.Inspection.Status = netsnapshot.StatusUnknown
	diagnostic.IPv4Routes.Inspection.Status = netsnapshot.StatusUnknown
	diagnostic.IPv4PolicyRules.Inspection.Status = netsnapshot.StatusUnknown
	opts := planTestOptions(t, storePath, diagnostic)
	opts.tunAllocationEvidence = func(context.Context) (netsnapshot.TunAllocationEvidence, error) {
		return netsnapshot.TunAllocationEvidence{
			IPv4Addresses:   []netip.Prefix{netip.MustParsePrefix("192.0.2.10/24")},
			IPv4Routes:      []netsnapshot.TunAllocationRoute{{Default: true, Table: 254}},
			IPv4PolicyRules: []netsnapshot.TunAllocationRule{{Priority: 100, Table: 60000}},
		}, nil
	}
	profileID := importPlanTestProfile(t, opts)

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"plan", "--mode", "tun", profileID}, &out, opts); err != nil {
		t.Fatalf("typed-authority TUN plan failed: %v", err)
	}
	if !strings.Contains(out.String(), "No changes were applied.") {
		t.Fatalf("unexpected TUN plan output: %q", out.String())
	}
}
