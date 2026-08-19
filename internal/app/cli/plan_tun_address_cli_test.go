package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func TestRunCLIPlanTunRendersDaemonOwnedAddressInAllFormats(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := planTestOptions(t, storePath, netsnapshot.FakeResolvedDesktop())
	profileID := importPlanTestProfile(t, opts)

	var compact bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"plan", "--mode", "tun", profileID, "--plain"}, &compact, opts); err != nil {
		t.Fatalf("compact TUN plan: %v", err)
	}
	assertContains(t, compact.String(), "Assign TUN IPv4 address")
	assertContains(t, compact.String(), planner.DefaultTunIPv4CIDR+" on "+netsnapshot.DefaultTunName)

	var verbose bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"plan", "--mode", "tun", profileID, "--verbose"}, &verbose, opts); err != nil {
		t.Fatalf("verbose TUN plan: %v", err)
	}
	assertContains(t, verbose.String(), "TUN address: assign "+planner.DefaultTunIPv4CIDR+" dev "+netsnapshot.DefaultTunName)
	assertContains(t, verbose.String(), "owner="+planner.TunAddressOwner)

	var rawJSON bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"plan", "--mode", "tun", profileID, "--json"}, &rawJSON, opts); err != nil {
		t.Fatalf("JSON TUN plan: %v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(rawJSON.Bytes(), &envelope); err != nil {
		t.Fatalf("decode JSON TUN plan: %v", err)
	}
	planJSON := envelope["plan"].(map[string]any)
	address := planJSON["tun_address"].(map[string]any)
	if address["cidr"] != planner.DefaultTunIPv4CIDR || address["action"] != planner.TunAddressActionAssign || address["owner"] != planner.TunAddressOwner {
		t.Fatalf("unexpected TUN address JSON: %#v", address)
	}
}

func TestRunCLIPlanTunFailsClosedWhenAddressAllocationPoolIsExhausted(t *testing.T) {
	s := netsnapshot.FakeResolvedDesktop()
	s.IPv4Routes.Routes = append(s.IPv4Routes.Routes, netsnapshot.Route{
		Status:      netsnapshot.StatusDetected,
		Family:      "ipv4",
		Destination: "198.18.0.0/15",
		Table:       "100",
		Interface:   "eth1",
	})
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := planTestOptions(t, storePath, s)
	profileID := importPlanTestProfile(t, opts)

	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"plan", "--mode", "tun", profileID, "--plain"}, &out, opts)
	if err == nil {
		t.Fatal("exhausted TUN address allocation pool must fail closed")
	}
	if !strings.Contains(err.Error(), "no collision-free TUN IPv4 address") {
		t.Fatalf("unexpected exhausted-allocation error: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("failed allocation must not render a misleading plan: %q", out.String())
	}
}
