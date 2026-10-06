package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/status"
)

func TestRunCLIConnectUsesCanonicalTunAndBlockForInactiveState(t *testing.T) {
	storePath := t.TempDir() + "/profiles.json"
	p := testConnectProfile()
	store, err := profile.NewStore(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(p); err != nil {
		t.Fatal(err)
	}

	var got api.ConnectRequest
	var out bytes.Buffer
	err = runWithOptions(context.Background(), []string{"connect", p.Name}, &out, options{
		profileStorePath: storePath,
		daemonStatus: func(context.Context) (status.Report, error) {
			return status.Report{Connection: "inactive"}, nil
		},
		connect: func(_ context.Context, req api.ConnectRequest) (api.LifecycleResponse, error) {
			got = req
			return api.LifecycleResponse{Connection: "active", Mode: req.Mode, ProfileName: p.Name, Proxy: "active", TUN: "enabled"}, nil
		},
	})
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	if got.Mode != planner.ModeTun || got.Handoff != api.HandoffBlock || got.Profile.ID != p.ID {
		t.Fatalf("canonical request = %+v", got)
	}
	if gotOut := out.String(); gotOut != "Connected\nProfile: test vless\nProtection: Active\n" {
		t.Fatalf("connect output = %q", gotOut)
	}
}


func TestRunCLIConnectReplacesDifferentHealthyPodlazTunSession(t *testing.T) {
	storePath := t.TempDir() + "/profiles.json"
	p := testConnectProfile()
	p.ID = "new-profile"
	p.Name = "New profile"
	store, _ := profile.NewStore(storePath)
	if err := store.Add(p); err != nil {
		t.Fatal(err)
	}

	var got api.ConnectRequest
	err := runWithOptions(context.Background(), []string{"connect", p.Name}, &bytes.Buffer{}, options{
		profileStorePath: storePath,
		daemonStatus: func(context.Context) (status.Report, error) {
			return status.Report{
				Connection:  "active",
				Mode:        planner.ModeTun,
				ProfileID:   "old-profile",
				ProfileName: "Old profile",
				TUN:         "enabled",
			}, nil
		},
		connect: func(_ context.Context, req api.ConnectRequest) (api.LifecycleResponse, error) {
			got = req
			return api.LifecycleResponse{Connection: "active", Mode: planner.ModeTun, Proxy: "active", TUN: "enabled"}, nil
		},
	})
	if err != nil {
		t.Fatalf("replacement connect failed: %v", err)
	}
	if got.Handoff != api.HandoffReplacePodlaz {
		t.Fatalf("handoff=%q, want %q", got.Handoff, api.HandoffReplacePodlaz)
	}
}

func TestRunCLIConnectDoesNotGrantReplacementOnUnhealthyOrAmbiguousState(t *testing.T) {
	p := testConnectProfile()
	for _, report := range []status.Report{
		{Connection: "unknown (inspection incomplete)", Mode: planner.ModeTun, ProfileID: "old"},
		{Connection: "active", Mode: planner.ModeProxyOnly, ProfileID: "old"},
		{Connection: "active", Mode: planner.ModeTun, ProfileID: "old", Candidates: []status.Candidate{{Kind: "transaction-state"}}},
	} {
		if got := canonicalConnectHandoff(report); got != api.HandoffBlock {
			t.Fatalf("report=%#v handoff=%q, want block", report, got)
		}
	}
}

func TestRunCLIConnectUsesSelectedProfile(t *testing.T) {
	storePath := t.TempDir() + "/profiles.json"
	p := testConnectProfile()
	store, _ := profile.NewStore(storePath)
	if err := store.Add(p); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Select(p.Name); err != nil {
		t.Fatal(err)
	}

	calls := 0
	err := runWithOptions(context.Background(), []string{"connect"}, &bytes.Buffer{}, options{
		profileStorePath: storePath,
		daemonStatus: func(context.Context) (status.Report, error) {
			return status.Report{Connection: "inactive"}, nil
		},
		connect: func(_ context.Context, req api.ConnectRequest) (api.LifecycleResponse, error) {
			calls++
			if req.Profile.ID != p.ID {
				t.Fatalf("connected profile %q, want %q", req.Profile.ID, p.ID)
			}
			return api.LifecycleResponse{Connection: "active", Mode: planner.ModeTun, Proxy: "active", TUN: "enabled"}, nil
		},
	})
	if err != nil || calls != 1 {
		t.Fatalf("selected connect err=%v calls=%d", err, calls)
	}
}

func TestRunCLIConnectSameHealthyIntentIsNoOp(t *testing.T) {
	storePath := t.TempDir() + "/profiles.json"
	p := testConnectProfile()
	store, _ := profile.NewStore(storePath)
	if err := store.Add(p); err != nil {
		t.Fatal(err)
	}

	calls := 0
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"connect", p.ID}, &out, options{
		profileStorePath: storePath,
		daemonStatus: func(context.Context) (status.Report, error) {
			return status.Report{
				Connection:  "active",
				Mode:        planner.ModeTun,
				ProfileID:   p.ID,
				ProfileName: p.Name,
				TUN:         "enabled",
			}, nil
		},
		connect: func(context.Context, api.ConnectRequest) (api.LifecycleResponse, error) {
			calls++
			return api.LifecycleResponse{}, nil
		},
	})
	if err != nil {
		t.Fatalf("idempotent connect failed: %v", err)
	}
	if calls != 0 {
		t.Fatalf("healthy same intent rebuilt session %d time(s)", calls)
	}
	if !strings.Contains(out.String(), "Protection: Active") {
		t.Fatalf("unexpected no-op output: %q", out.String())
	}
}

func TestRunCLIConnectRejectsOperatorLifecycleFlags(t *testing.T) {
	for _, args := range [][]string{
		{"connect", "--mode=tun", "profile"},
		{"connect", "--handoff=replace-podlaz", "profile"},
		{"connect", "--json", "profile"},
	} {
		err := run(context.Background(), args, &bytes.Buffer{})
		if err == nil || ExitCode(err) != 2 {
			t.Fatalf("args=%v err=%v exit=%d, want usage error", args, err, ExitCode(err))
		}
	}
}

func TestRunDebugProxyIsExplicitReducedProtection(t *testing.T) {
	storePath := t.TempDir() + "/profiles.json"
	p := testConnectProfile()
	store, _ := profile.NewStore(storePath)
	if err := store.Add(p); err != nil {
		t.Fatal(err)
	}

	var got api.ConnectRequest
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"debug", "proxy", p.Name}, &out, options{
		profileStorePath: storePath,
		connect: func(_ context.Context, req api.ConnectRequest) (api.LifecycleResponse, error) {
			got = req
			return api.LifecycleResponse{Connection: "active", Mode: req.Mode, Proxy: "active", TUN: "disabled"}, nil
		},
	})
	if err != nil {
		t.Fatalf("debug proxy: %v", err)
	}
	if got.Mode != planner.ModeProxyOnly || got.Handoff != api.HandoffBlock {
		t.Fatalf("debug proxy request = %+v", got)
	}
	for _, want := range []string{"Connected with reduced protection", "Profile: test vless", "Protection: Proxy only"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("debug proxy output missing %q: %q", want, out.String())
		}
	}
}

func TestRunCLIDisconnectRendersProductSuccessOnly(t *testing.T) {
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"disconnect"}, &out, options{
		disconnect: func(context.Context) (api.LifecycleResponse, error) {
			return api.LifecycleResponse{Connection: "inactive", Proxy: "inactive", TUN: "disabled"}, nil
		},
	})
	if err != nil {
		t.Fatalf("disconnect failed: %v", err)
	}
	if got := out.String(); got != "Disconnected\n" {
		t.Fatalf("disconnect output = %q", got)
	}
}

func testConnectProfile() profile.Profile {
	return profile.Profile{
		ID:           "test-vless",
		Name:         "test vless",
		Source:       profile.SourceImportedURI,
		Engine:       profile.EngineXray,
		Server:       "example.com",
		Port:         443,
		Protocol:     "vless",
		UserIdentity: testVLESSUserIdentity(),
		Transport:    "tcp",
		Security:     "tls",
		Encryption:   "none",
		ServerName:   "example.com",
	}
}

func testVLESSUserIdentity() string {
	part := "1111"
	return fmt.Sprintf("%s%s-%s-%s-%s-%s%s%s", part, part, part, part, part, part, part, part)
}
