package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func TestCollectTunAllocationEvidenceUsesDedicatedAuthorityCollector(t *testing.T) {
	want := netsnapshot.TunAllocationEvidence{
		IPv4Routes:      []netsnapshot.TunAllocationRoute{{Default: true, Table: 254}},
		IPv4PolicyRules: []netsnapshot.TunAllocationRule{{Priority: 100, Table: 60000}},
	}
	calls := 0
	manager := &XrayManager{
		allocationEvidenceCollector: func(context.Context) (netsnapshot.TunAllocationEvidence, error) {
			calls++
			return want, nil
		},
	}

	got, err := manager.collectTunAllocationEvidence(context.Background(), netsnapshot.Snapshot{})
	if err != nil {
		t.Fatalf("collectTunAllocationEvidence() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("dedicated allocation authority collector calls = %d, want 1", calls)
	}
	if len(got.IPv4Routes) != 1 || got.IPv4Routes[0].Table != 254 || len(got.IPv4PolicyRules) != 1 || got.IPv4PolicyRules[0].Priority != 100 {
		t.Fatalf("unexpected allocation authority: %#v", got)
	}
}

func TestConnectTunFailsClosedWhenTypedAllocationAuthorityIsUnavailable(t *testing.T) {
	oldPreflight := preflightNativeTunSupport
	oldValidateDeps := validateTunRuntimeDependenciesHook
	t.Cleanup(func() {
		preflightNativeTunSupport = oldPreflight
		validateTunRuntimeDependenciesHook = oldValidateDeps
	})
	preflightNativeTunSupport = func(context.Context, string, coreExecutionIdentity) error { return nil }
	validateTunRuntimeDependenciesHook = func() error { return nil }

	calls := 0
	manager := &XrayManager{
		RuntimeDir: t.TempDir(),
		XrayPath:   writeFakeXray(t, "#!/bin/sh\nexit 0\n"),
		snapshotCollector: func(context.Context, netsnapshot.Options) netsnapshot.Snapshot {
			return netsnapshot.FakeResolvedDesktop()
		},
		allocationEvidenceCollector: func(context.Context) (netsnapshot.TunAllocationEvidence, error) {
			calls++
			return netsnapshot.TunAllocationEvidence{}, errors.New("synthetic rtnetlink failure")
		},
	}
	req := connectRequestForTest()
	req.Mode = planner.ModeTun
	req.Handoff = api.HandoffBlock

	_, err := manager.Connect(context.Background(), req)
	if err == nil {
		t.Fatal("expected unavailable allocation authority to block TUN connect")
	}
	if calls != 1 {
		t.Fatalf("allocation authority collector calls = %d, want 1", calls)
	}
	if !strings.Contains(err.Error(), "TUN allocation evidence") || !strings.Contains(err.Error(), "synthetic rtnetlink failure") {
		t.Fatalf("unexpected allocation authority error: %v", err)
	}
}
