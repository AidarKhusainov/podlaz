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
)

func TestRunCLIConnectAcceptsHandoffPolicies(t *testing.T) {
	storePath := t.TempDir() + "/profiles.json"
	p := testConnectProfile()
	store, err := profile.NewStore(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(p); err != nil {
		t.Fatal(err)
	}

	for _, policy := range api.HandoffPolicies() {
		var gotHandoff string
		err := runWithOptions(context.Background(), []string{"connect", "--mode=tun", "--handoff=" + policy, p.ID}, &bytes.Buffer{}, options{
			profileStorePath: storePath,
			connect: func(_ context.Context, req api.ConnectRequest) (api.LifecycleResponse, error) {
				gotHandoff = req.Handoff
				return api.LifecycleResponse{Connection: "active", Mode: req.Mode, ProfileName: p.Name, Proxy: "ok", TUN: "enabled"}, nil
			},
		})
		if err != nil {
			t.Fatalf("connect with handoff %q failed: %v", policy, err)
		}
		if gotHandoff != policy {
			t.Fatalf("handoff = %q, want %q", gotHandoff, policy)
		}
	}
}

func TestRunCLIConnectRendersProductSuccessOnly(t *testing.T) {
	storePath := t.TempDir() + "/profiles.json"
	p := testConnectProfile()
	store, err := profile.NewStore(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(p); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = runWithOptions(context.Background(), []string{"connect", "--mode=tun", p.ID}, &out, options{
		profileStorePath: storePath,
		connect: func(_ context.Context, req api.ConnectRequest) (api.LifecycleResponse, error) {
			return api.LifecycleResponse{
				Connection: "active", Mode: req.Mode, ProfileName: p.Name,
				Proxy: "active", TUN: "active", Routes: "private", DNS: "private", Firewall: "private", RuntimeConfigPath: "/private/config",
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	got := out.String()
	for _, want := range []string{"Connected", "Profile: test vless", "Mode: TUN"} {
		if !strings.Contains(got, want) {
			t.Fatalf("connect output missing %q: %q", want, got)
		}
	}
	for _, forbidden := range []string{"Proxy:", "TUN:", "Routes:", "DNS:", "Firewall:", "Runtime config:", "Profile ID:"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("connect output leaked %q: %q", forbidden, got)
		}
	}
}

func TestRunCLIConnectRejectsUnsupportedHandoffPolicy(t *testing.T) {
	var out bytes.Buffer
	err := run(context.Background(), []string{"connect", "--mode=tun", "--handoff=unsupported", "profile-id"}, &out)
	if err == nil {
		t.Fatal("expected unsupported handoff policy to fail")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("expected exit code 2, got %d", got)
	}
	if !strings.Contains(err.Error(), "unsupported handoff policy") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunCLIDisconnectRendersProductSuccessOnly(t *testing.T) {
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"disconnect"}, &out, options{
		disconnect: func(context.Context) (api.LifecycleResponse, error) {
			return api.LifecycleResponse{Connection: "inactive", Proxy: "inactive", TUN: "disabled", Routes: "removed", DNS: "restored", Firewall: "removed"}, nil
		},
	})
	if err != nil {
		t.Fatalf("disconnect failed: %v", err)
	}
	if got := out.String(); got != "Disconnected\n" {
		t.Fatalf("disconnect output = %q, want concise product output", got)
	}
}

func TestRunCLIConnectRejectsUnknownMode(t *testing.T) {
	var out bytes.Buffer
	err := run(context.Background(), []string{"connect", "--mode", "unknown", "profile-id"}, &out)
	if err == nil {
		t.Fatal("expected unsupported connect mode to fail")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("expected exit code 2, got %d", got)
	}
	if !strings.Contains(err.Error(), "unsupported connect mode") {
		t.Fatalf("unexpected error: %v", err)
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

var _ = planner.ModeTun
