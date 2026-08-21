package cli

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/profile"
)

func TestRunAutostartEnableLoadsValidatedProfileWithoutConnecting(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	store, err := profile.NewStore(storePath)
	if err != nil {
		t.Fatal(err)
	}
	p := testConnectProfile()
	p.Name = "Example VPN"
	if err := store.Add(p); err != nil {
		t.Fatal(err)
	}

	var got api.AutostartConfigureRequest
	connectCalls := 0
	var out bytes.Buffer
	err = runWithOptions(context.Background(), []string{"autostart", "enable", "--mode=tun", p.ID}, &out, options{
		profileStorePath: storePath,
		connect: func(context.Context, api.ConnectRequest) (api.LifecycleResponse, error) {
			connectCalls++
			return api.LifecycleResponse{}, errors.New("connect must not be called")
		},
		autostartEnable: func(_ context.Context, request api.AutostartConfigureRequest) (api.AutostartStatusResponse, error) {
			got = request
			return api.AutostartStatusResponse{Enabled: true, Mode: request.Mode, ProfileName: request.Profile.Name}, nil
		},
	})
	if err != nil {
		t.Fatalf("autostart enable: %v", err)
	}
	if connectCalls != 0 {
		t.Fatalf("autostart enable called normal connect %d time(s)", connectCalls)
	}
	if got.Mode != "tun" || got.Profile.ID != p.ID || got.Profile.Name != p.Name {
		t.Fatalf("autostart request = %+v", got)
	}
	if got.Profile.UserIdentity != p.UserIdentity {
		t.Fatalf("autostart snapshot did not contain validated connection material")
	}
	if gotJSON := out.String(); gotJSON != "Autostart: Enabled for next boot\nProfile: Example VPN\nMode: TUN\n" {
		t.Fatalf("autostart enable output = %q", gotJSON)
	}
}

func TestRunAutostartEnableUsesSameProfileValidationAsConnect(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	store, err := profile.NewStore(storePath)
	if err != nil {
		t.Fatal(err)
	}
	p := testConnectProfile()
	p.Server = ""
	if err := store.Add(p); err == nil {
		// Store validation may reject the invalid profile before the CLI can load
		// it. Write a valid profile and then validate the mode-specific path below.
		p = testConnectProfile()
		if err := store.Add(p); err != nil {
			t.Fatal(err)
		}
	}

	// xray-json is only valid when its canonical payload is present; this is the
	// same planner validation run by explicit connect.
	p = testConnectProfile()
	p.ID = "invalid-autostart-profile"
	p.Protocol = "xray-json"
	p.RealitySpiderX = ""
	if err := store.Add(p); err != nil {
		// Profile-store validation is allowed to reject it earlier; the behavior
		// under test is that daemon policy is never called for invalid material.
		return
	}
	called := false
	err = runWithOptions(context.Background(), []string{"autostart", "enable", p.ID}, &bytes.Buffer{}, options{
		profileStorePath: storePath,
		autostartEnable: func(context.Context, api.AutostartConfigureRequest) (api.AutostartStatusResponse, error) {
			called = true
			return api.AutostartStatusResponse{}, nil
		},
	})
	if err == nil {
		t.Fatal("invalid profile unexpectedly configured autostart")
	}
	if called {
		t.Fatal("invalid profile reached daemon autostart configuration")
	}
}

func TestRunAutostartDisableAndStatusAreConcise(t *testing.T) {
	var disableOut bytes.Buffer
	err := runWithOptions(context.Background(), []string{"autostart", "disable"}, &disableOut, options{
		autostartDisable: func(context.Context) (api.AutostartStatusResponse, error) {
			return api.AutostartStatusResponse{Enabled: false}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if disableOut.String() != "Autostart: Disabled\n" {
		t.Fatalf("disable output = %q", disableOut.String())
	}

	var statusOut bytes.Buffer
	err = runWithOptions(context.Background(), []string{"autostart", "status"}, &statusOut, options{
		autostartStatus: func(context.Context) (api.AutostartStatusResponse, error) {
			return api.AutostartStatusResponse{Enabled: true, Mode: "proxy-only", ProfileName: "Example VPN"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if statusOut.String() != "Autostart: Enabled for next boot\nProfile: Example VPN\nMode: Proxy only\n" {
		t.Fatalf("status output = %q", statusOut.String())
	}
}

func TestRunAutostartRejectsUnreviewedJSONAndInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		{"autostart", "status", "--json"},
		{"autostart", "enable", "--json", "profile"},
		{"autostart", "enable", "--mode=invalid", "profile"},
		{"autostart", "disable", "extra"},
		{"autostart", "unknown"},
	} {
		err := runWithOptions(context.Background(), args, &bytes.Buffer{}, options{})
		if err == nil || ExitCode(err) != 2 {
			t.Fatalf("args=%v error=%v exit=%d, want usage error", args, err, ExitCode(err))
		}
	}
}

func TestCompletionAutostartEnableCompletesProfilesAndModes(t *testing.T) {
	dir := t.TempDir()
	opts := options{profileStorePath: filepath.Join(dir, "profiles.json")}
	profileID := storeCompletionProfile(t, opts, "autostart-example", "Autostart Example")

	commands := completepodlaz(completionRequest{Shell: "bash", Cursor: 2, Words: []string{"podlaz", "autostart", ""}}, opts)
	for _, want := range []string{"enable", "disable", "status"} {
		assertCompletionCandidate(t, commands, want)
	}
	ids := completepodlaz(completionRequest{Shell: "zsh", Cursor: 3, Words: []string{"podlaz", "autostart", "enable", ""}}, opts)
	assertCompletionCandidateDescription(t, ids, profileID, "Autostart Example")
	modes := completepodlaz(completionRequest{Shell: "fish", Cursor: 4, Words: []string{"podlaz", "autostart", "enable", "--mode", ""}}, opts)
	assertCompletionCandidate(t, modes, "proxy-only")
	assertCompletionCandidate(t, modes, "tun")
}

func TestAutostartHelpDocumentsFutureBootScope(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"help", "autostart"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"autostart enable", "autostart disable", "autostart status", "next boot", "does not connect immediately"} {
		if !strings.Contains(strings.ToLower(out.String()), strings.ToLower(want)) {
			t.Fatalf("autostart help missing %q: %q", want, out.String())
		}
	}
}
