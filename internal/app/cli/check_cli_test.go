package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	profilecheck "github.com/AidarKhusainov/podlaz/internal/check"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/status"
)

func TestRunCLICheckProductionPathStartsProbesAndCleansUpOwnedProxy(t *testing.T) {
	storePath := t.TempDir() + "/profiles.json"
	p := testConnectProfile()
	addCheckTestProfile(t, storePath, p)

	connection := "inactive"
	connectCalled := false
	disconnectCalled := false
	probeCalls := map[string]int{}
	proxyLine := "listening on 127.0.0.1:1080 (SOCKS), 127.0.0.1:8080 (HTTP)"
	statusForState := func() status.Report {
		report := status.Report{Daemon: "running", Service: api.ServiceManual, Connection: connection, Proxy: "inactive", TUN: "disabled"}
		if connection == "active" {
			report.Mode = planner.ModeProxyOnly
			report.ProfileID = p.ID
			report.ProfileName = p.Name
			report.Proxy = proxyLine
			report.RuntimeConfigPath = planner.DefaultRuntimeConfigPath
		}
		return report
	}

	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"check", p.ID, "--target", "telegram"}, &out, options{
		profileStorePath: storePath,
		daemonStatus: func(context.Context) (status.Report, error) {
			return statusForState(), nil
		},
		connect: func(_ context.Context, req api.ConnectRequest) (api.LifecycleResponse, error) {
			connectCalled = true
			if req.Profile.ID != p.ID || req.Mode != planner.ModeProxyOnly {
				t.Fatalf("unexpected connect request: profile=%s mode=%s", req.Profile.ID, req.Mode)
			}
			connection = "active"
			return api.LifecycleResponse{Connection: "active", Mode: planner.ModeProxyOnly, ProfileID: p.ID, ProfileName: p.Name, Proxy: proxyLine, TUN: "disabled", Routes: "not modified", DNS: "not modified", Firewall: "not modified", RuntimeConfigPath: planner.DefaultRuntimeConfigPath}, nil
		},
		disconnect: func(context.Context) (api.LifecycleResponse, error) {
			disconnectCalled = true
			connection = "inactive"
			return api.LifecycleResponse{Connection: "inactive", Proxy: "inactive", TUN: "disabled"}, nil
		},
		checkProbes: successfulCheckProbes(probeCalls),
	})
	if err != nil {
		t.Fatalf("check production path failed: %v\n%s", err, out.String())
	}
	if !connectCalled || !disconnectCalled {
		t.Fatalf("expected connect and ownership-checked cleanup, connect=%v disconnect=%v", connectCalled, disconnectCalled)
	}
	for _, name := range []string{"server_tcp", "socks", "http"} {
		if probeCalls[name] == 0 {
			t.Fatalf("expected %s probe to run, calls=%#v", name, probeCalls)
		}
	}
	if got := out.String(); !strings.Contains(got, "Result: ok") || !strings.Contains(got, "Telegram: reachable") {
		t.Fatalf("expected successful check output, got %q", got)
	}
}

func TestRunCLICheckRendersInjectedProfileReport(t *testing.T) {
	storePath := t.TempDir() + "/profiles.json"
	p := testConnectProfile()
	addCheckTestProfile(t, storePath, p)

	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"check", p.ID, "--target", "telegram"}, &out, options{
		profileStorePath: storePath,
		check: func(_ context.Context, p profile.Profile, execOpts checkExecutionOptions) profileCheckReport {
			return successfulCheckReport(p, execOpts.Targets)
		},
	})
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	got := out.String()
	for _, want := range []string{"Profile check: test vless", "Mode: proxy-only", "Profile validation: ok", "Telegram: reachable", "Result: ok"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected output to contain %q, got %q", want, got)
		}
	}
}

func TestRunCLICheckJSONIncludesStableSchema(t *testing.T) {
	storePath := t.TempDir() + "/profiles.json"
	p := testConnectProfile()
	addCheckTestProfile(t, storePath, p)

	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"check", p.ID, "--json"}, &out, options{
		profileStorePath: storePath,
		check: func(_ context.Context, p profile.Profile, execOpts checkExecutionOptions) profileCheckReport {
			return successfulCheckReport(p, execOpts.Targets)
		},
	})
	if err != nil {
		t.Fatalf("check --json failed: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if payload["schema_version"] != profilecheck.SchemaVersion || payload["status"] != "ok" {
		t.Fatalf("unexpected JSON payload: %#v", payload)
	}
}

func TestRunCLICheckRejectsUnsupportedProfileBeforeDaemon(t *testing.T) {
	storePath := t.TempDir() + "/profiles.json"
	p := testConnectProfile()
	p.ID = "unsupported-quic"
	p.Transport = "quic"
	addCheckTestProfile(t, storePath, p)

	calledDaemon := false
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"check", p.ID}, &out, options{
		profileStorePath: storePath,
		connect: func(context.Context, api.ConnectRequest) (api.LifecycleResponse, error) {
			calledDaemon = true
			return api.LifecycleResponse{}, nil
		},
	})
	if err == nil {
		t.Fatal("expected unsupported profile check to fail")
	}
	if calledDaemon {
		t.Fatal("unsupported profile was sent to the daemon")
	}
	if got := ExitCode(err); got != 3 {
		t.Fatalf("expected diagnostic exit code 3, got %d", got)
	}
	if got := out.String(); !strings.Contains(got, "unsupported proxy-only VLESS transport") || !strings.Contains(got, "Result: fail") {
		t.Fatalf("expected validation failure in output, got %q", got)
	}
}

func TestRunCLICheckAllRendersBatchSummary(t *testing.T) {
	storePath := t.TempDir() + "/profiles.json"
	first := testConnectProfile()
	second := testConnectProfile()
	second.ID = "test-vless-2"
	second.Name = "test vless 2"
	addCheckTestProfiles(t, storePath, first, second)

	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"check", "--all"}, &out, options{
		profileStorePath: storePath,
		check: func(_ context.Context, p profile.Profile, execOpts checkExecutionOptions) profileCheckReport {
			return successfulCheckReport(p, execOpts.Targets)
		},
	})
	if err != nil {
		t.Fatalf("check --all failed: %v", err)
	}
	got := out.String()
	for _, want := range []string{"Profile connectivity checks", "Concurrency: 1", "test-vless", "test-vless-2", "Result: ok"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected batch output to contain %q, got %q", want, got)
		}
	}
}

func successfulCheckReport(p profile.Profile, targets []profilecheck.Target) profileCheckReport {
	services := make([]profilecheck.ProbeResult, 0, len(targets))
	for _, target := range targets {
		services = append(services, profilecheck.OK(target.ID, target.DisplayName, 10*time.Millisecond, "HTTP 204"))
	}
	return profileCheckReport{
		SchemaVersion: profilecheck.SchemaVersion,
		Status:        "ok",
		Warnings:      []string{},
		Errors:        []string{},
		Profile:       p,
		Mode:          planner.ModeProxyOnly,
		Validation:    profilecheck.OK("profile_validation", "Profile validation", 0, "renderable for proxy-only"),
		Daemon:        profilecheck.OK("daemon", "Daemon", 0, "running; connection inactive"),
		ProxyStartup:  profilecheck.OK("proxy_startup", "Proxy startup", 0, "active"),
		ServerTCP:     profilecheck.OK("server_tcp", "Server TCP handshake", 10*time.Millisecond, "tcp connect"),
		SOCKSEgress:   profilecheck.OK("socks_egress", "SOCKS egress", 10*time.Millisecond, "HTTP 204"),
		HTTPEgress:    profilecheck.OK("http_proxy_egress", "HTTP proxy egress", 10*time.Millisecond, "HTTP 204"),
		Services:      services,
	}
}

func successfulCheckProbes(calls map[string]int) checkProbeRunner {
	return checkProbeRunner{
		serverTCP: func(context.Context, string, time.Duration) profilecheck.ProbeResult {
			calls["server_tcp"]++
			return profilecheck.OK("server_tcp", "Server TCP handshake", time.Millisecond, "tcp connect to profile server")
		},
		socks: func(context.Context, profilecheck.Endpoint, profilecheck.Target, time.Duration) profilecheck.ProbeResult {
			calls["socks"]++
			return profilecheck.OK("socks_egress", "SOCKS egress", time.Millisecond, "HTTP 204")
		},
		httpProxy: func(_ context.Context, _ profilecheck.Endpoint, target profilecheck.Target, _ time.Duration) profilecheck.ProbeResult {
			calls["http"]++
			return profilecheck.OK(target.ID, target.DisplayName, time.Millisecond, "HTTP 204")
		},
	}
}

func addCheckTestProfile(t *testing.T, storePath string, p profile.Profile) {
	t.Helper()
	addCheckTestProfiles(t, storePath, p)
}

func addCheckTestProfiles(t *testing.T, storePath string, profiles ...profile.Profile) {
	t.Helper()
	store, err := profile.NewStore(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddProfiles(profiles); err != nil {
		t.Fatal(err)
	}
}
