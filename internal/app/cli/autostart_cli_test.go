package cli

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
)

func TestRunAutostartEnableUsesSelectedCanonicalVPNWithoutConnecting(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	store, _ := profile.NewStore(storePath)
	p := testConnectProfile()
	p.Name = "Example VPN"
	if err := store.Add(p); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Select(p.Name); err != nil {
		t.Fatal(err)
	}

	var got api.AutostartConfigureRequest
	connectCalls := 0
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"autostart", "enable"}, &out, options{
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
		t.Fatalf("autostart enable called connect %d time(s)", connectCalls)
	}
	if got.Mode != planner.ModeTun || got.Profile.ID != p.ID || got.Profile.Name != p.Name {
		t.Fatalf("autostart request = %+v", got)
	}
	if out.String() != "Autostart: Enabled for next boot\nProfile: Example VPN\n" {
		t.Fatalf("autostart output = %q", out.String())
	}
}

func TestRunAutostartEnableExplicitProfileDoesNotChangeSelection(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	store, _ := profile.NewStore(storePath)
	first := testConnectProfile()
	first.ID, first.Name = "first", "First"
	second := testConnectProfile()
	second.ID, second.Name = "second", "Second"
	if err := store.Add(first); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(second); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Select(first.Name); err != nil {
		t.Fatal(err)
	}

	err := runWithOptions(context.Background(), []string{"autostart", "enable", second.Name}, &bytes.Buffer{}, options{
		profileStorePath: storePath,
		autostartEnable: func(_ context.Context, request api.AutostartConfigureRequest) (api.AutostartStatusResponse, error) {
			if request.Profile.ID != second.ID {
				t.Fatalf("autostart profile = %q", request.Profile.ID)
			}
			return api.AutostartStatusResponse{Enabled: true, Mode: request.Mode, ProfileName: request.Profile.Name}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := store.ResolveSelected()
	if err != nil {
		t.Fatal(err)
	}
	if selected.ID != first.ID {
		t.Fatalf("explicit autostart changed selection to %q", selected.ID)
	}
}

func TestRunAutostartDisableAndProxyLegacyStatusAreConcise(t *testing.T) {
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
			return api.AutostartStatusResponse{Enabled: true, Mode: planner.ModeProxyOnly, ProfileName: "Example VPN"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if statusOut.String() != "Autostart: Enabled for next boot\nProfile: Example VPN\nProtection: Proxy only\n" {
		t.Fatalf("status output = %q", statusOut.String())
	}
}

func TestRunAutostartRejectsRemovedPolicyFlags(t *testing.T) {
	for _, args := range [][]string{
		{"autostart", "status", "--json"},
		{"autostart", "enable", "--mode=tun", "profile"},
		{"autostart", "disable", "extra"},
		{"autostart", "unknown"},
	} {
		err := runWithOptions(context.Background(), args, &bytes.Buffer{}, options{})
		if err == nil || ExitCode(err) != 2 {
			t.Fatalf("args=%v error=%v exit=%d, want usage error", args, err, ExitCode(err))
		}
	}
}

func TestCompletionAutostartEnableCompletesProfileNames(t *testing.T) {
	dir := t.TempDir()
	opts := options{profileStorePath: filepath.Join(dir, "profiles.json")}
	storeCompletionProfile(t, opts, "autostart-example", "Autostart Example")

	commands := completepodlaz(completionRequest{Shell: "bash", Cursor: 2, Words: []string{"podlaz", "autostart", ""}}, opts)
	for _, want := range []string{"enable", "disable", "status"} {
		assertCompletionCandidate(t, commands, want)
	}
	profiles := completepodlaz(completionRequest{Shell: "zsh", Cursor: 3, Words: []string{"podlaz", "autostart", "enable", ""}}, opts)
	assertCompletionCandidate(t, profiles, "Autostart Example")
}

func TestAutostartHelpDocumentsFutureBootScope(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"help", "autostart"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"autostart enable", "autostart disable", "autostart status", "does not connect immediately", "profile use"} {
		if !strings.Contains(strings.ToLower(out.String()), strings.ToLower(want)) {
			t.Fatalf("autostart help missing %q: %q", want, out.String())
		}
	}
}
