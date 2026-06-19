package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
)

func TestRunCLIConnectStartsStoredProfileViaDaemon(t *testing.T) {
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
	var gotProfile profile.Profile
	var gotMode string
	err = runWithOptions(context.Background(), []string{"connect", "--mode", "proxy-only", p.ID}, &out, options{
		profileStorePath: storePath,
		connect: func(_ context.Context, p profile.Profile, mode string) (api.LifecycleResponse, error) {
			gotProfile = p
			gotMode = mode
			return api.LifecycleResponse{
				Connection:        "active",
				Mode:              mode,
				Proxy:             "listening on 127.0.0.1:1080 (SOCKS), 127.0.0.1:8080 (HTTP)",
				TUN:               "disabled",
				Routes:            "not modified",
				DNS:               "not modified",
				Firewall:          "not modified",
				RuntimeConfigPath: "/run/podlaz/generated/xray.json",
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	if gotMode != planner.ModeProxyOnly {
		t.Fatalf("expected proxy-only mode, got %q", gotMode)
	}
	if gotProfile.ID != p.ID {
		t.Fatalf("expected profile %q, got %q", p.ID, gotProfile.ID)
	}
	for _, text := range []string{
		"podlaz connection started",
		"Connection: active",
		"Mode: proxy-only",
		"Proxy: listening on 127.0.0.1:1080",
		"TUN: disabled",
		"Routes: not modified",
		"DNS: not modified",
		"Firewall: not modified",
	} {
		if !strings.Contains(out.String(), text) {
			t.Fatalf("expected output to contain %q, got %q", text, out.String())
		}
	}
}

func TestRunCLIConnectAcceptsTunModeViaDaemon(t *testing.T) {
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
	var gotMode string
	err = runWithOptions(context.Background(), []string{"connect", "--mode=tun", p.ID}, &out, options{
		profileStorePath: storePath,
		connect: func(_ context.Context, _ profile.Profile, mode string) (api.LifecycleResponse, error) {
			gotMode = mode
			return api.LifecycleResponse{
				Connection: "active",
				Mode:       mode,
				Proxy:      "not started in this executor slice",
				TUN:        "enabled (podlaz0)",
				Routes:     "applied 2 route(s) and 2 policy rule(s)",
				DNS:        "not modified",
				Firewall:   "not modified",
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("connect --mode tun failed: %v", err)
	}
	if gotMode != planner.ModeTun {
		t.Fatalf("expected tun mode, got %q", gotMode)
	}
	for _, text := range []string{"Mode: tun", "TUN: enabled (podlaz0)", "Routes: applied 2 route(s)"} {
		if !strings.Contains(out.String(), text) {
			t.Fatalf("expected output to contain %q, got %q", text, out.String())
		}
	}
}

func TestRunCLIDisconnectIsRenderedAsInactive(t *testing.T) {
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"disconnect"}, &out, options{
		disconnect: func(context.Context) (api.LifecycleResponse, error) {
			return api.LifecycleResponse{
				Connection: "inactive",
				Proxy:      "inactive",
				TUN:        "disabled",
				Routes:     "not modified",
				DNS:        "not modified",
				Firewall:   "not modified",
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("disconnect failed: %v", err)
	}
	for _, text := range []string{"podlaz disconnected", "Connection: inactive", "Proxy: inactive"} {
		if !strings.Contains(out.String(), text) {
			t.Fatalf("expected output to contain %q, got %q", text, out.String())
		}
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
		UserIdentity: "11111111-1111-1111-1111-111111111111",
		Transport:    "tcp",
		Security:     "tls",
		Encryption:   "none",
		ServerName:   "example.com",
	}
}
