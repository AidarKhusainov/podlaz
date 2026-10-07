package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/testfixtures"
)

const groupedCLIProfileID = "xray-json-redaction"

func TestRunCLICanonicalConnectDispatchesGroupedProviderToTunDaemon(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{profileStorePath: storePath}
	addGroupedCLIProfile(t, opts)

	var request api.ConnectRequest
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"connect", "Grouped provider"}, &out, options{
		profileStorePath: storePath,
		connect: func(_ context.Context, req api.ConnectRequest) (api.LifecycleResponse, error) {
			request = req
			return api.LifecycleResponse{
				Connection: "active",
				Mode:       planner.ModeTun,
				Proxy:      "disabled",
				TUN:        "active",
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("grouped provider canonical connect failed: %v", err)
	}
	if request.Mode != planner.ModeTun {
		t.Fatalf("canonical grouped provider mode=%q, want %q", request.Mode, planner.ModeTun)
	}
	if request.Profile.Protocol != profile.ProtocolXrayJSON {
		t.Fatalf("canonical grouped provider protocol=%q", request.Profile.Protocol)
	}
	if request.Profile.Server != "" || request.Profile.Port != 0 || request.Profile.UserIdentity != "" {
		t.Fatalf("canonical grouped provider acquired flattened endpoint authority: %#v", request.Profile)
	}
	if strings.TrimSpace(request.Profile.RealitySpiderX) == "" {
		t.Fatal("canonical grouped provider request lost schema-opaque source authority")
	}
	assertGroupedCLINoSensitiveMaterial(t, out.String())
}

func TestRunCLIGroupedProviderProfileOutputRedaction(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{profileStorePath: storePath}
	addGroupedCLIProfile(t, opts)

	for _, args := range [][]string{
		{"profile", "list"},
		{"profile", "show", groupedCLIProfileID},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var out bytes.Buffer
			if err := runWithOptions(context.Background(), args, &out, opts); err != nil {
				t.Fatalf("%v failed: %v", args, err)
			}
			assertGroupedCLINoSensitiveMaterial(t, out.String())
		})
	}
}

func TestRunCLIDebugProxySupportsGroupedProviderWithoutLeakingSource(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	addGroupedCLIProfile(t, options{profileStorePath: storePath})

	var request api.ConnectRequest
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"debug", "proxy", "Grouped provider"}, &out, options{
		profileStorePath: storePath,
		connect: func(_ context.Context, req api.ConnectRequest) (api.LifecycleResponse, error) {
			request = req
			return api.LifecycleResponse{Connection: "active", Mode: planner.ModeProxyOnly, Proxy: "active", TUN: "disabled"}, nil
		},
	})
	if err != nil {
		t.Fatalf("debug proxy failed: %v", err)
	}
	if request.Mode != planner.ModeProxyOnly {
		t.Fatalf("debug proxy mode=%q", request.Mode)
	}
	if !strings.Contains(out.String(), "Protection: Proxy only") {
		t.Fatalf("debug proxy output=%q", out.String())
	}
	assertGroupedCLINoSensitiveMaterial(t, out.String())
}

func addGroupedCLIProfile(t *testing.T, opts options) {
	t.Helper()
	store, err := profile.NewStore(opts.profileStorePath)
	if err != nil {
		t.Fatalf("create profile store: %v", err)
	}
	p := profile.Profile{
		ID:             groupedCLIProfileID,
		Name:           "Grouped provider",
		Source:         profile.SourceSubscription,
		Engine:         profile.EngineXray,
		Protocol:       profile.ProtocolXrayJSON,
		RealitySpiderX: testfixtures.GroupedProviderXrayJSON(),
	}
	if err := store.Add(p); err != nil {
		t.Fatalf("add grouped profile: %v", err)
	}
}

func assertGroupedCLINoSensitiveMaterial(t *testing.T, got string) {
	t.Helper()
	for _, secret := range []string{
		testfixtures.GroupedXrayUserID,
		testfixtures.GroupedXraySecretToken,
		testfixtures.GroupedXrayRuntimeSentinel,
		`"outbounds"`,
		`"routing"`,
		`"vnext"`,
		"auto.edge.invalid",
		"ai.edge.invalid",
		"tg.edge.invalid",
	} {
		if strings.Contains(got, secret) {
			t.Fatalf("CLI output leaked grouped provider material %q in %q", secret, got)
		}
	}
}
