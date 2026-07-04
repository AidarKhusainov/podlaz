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
				return api.LifecycleResponse{Connection: "active", Mode: req.Mode, Proxy: "ok", TUN: "enabled"}, nil
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

func TestRunCLIDisconnectIsRenderedAsInactive(t *testing.T) {
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"disconnect"}, &out, options{
		disconnect: func(context.Context) (api.LifecycleResponse, error) {
			return api.LifecycleResponse{Connection: "inactive", Proxy: "inactive", TUN: "disabled"}, nil
		},
	})
	if err != nil {
		t.Fatalf("disconnect failed: %v", err)
	}
	if !strings.Contains(out.String(), "podlaz disconnected") || !strings.Contains(out.String(), "Connection: inactive") {
		t.Fatalf("unexpected disconnect output: %q", out.String())
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
