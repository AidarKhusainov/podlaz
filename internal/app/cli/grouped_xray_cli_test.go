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

func TestRunCLICanonicalConnectRejectsGroupedProviderBeforeDaemon(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{profileStorePath: storePath}
	addGroupedCLIProfile(t, opts)

	calledDaemon := false
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"connect", "Grouped provider"}, &out, options{
		profileStorePath: storePath,
		connect: func(context.Context, api.ConnectRequest) (api.LifecycleResponse, error) {
			calledDaemon = true
			return api.LifecycleResponse{}, nil
		},
	})
	if err == nil {
		t.Fatal("expected grouped provider canonical connect to fail")
	}
	if calledDaemon {
		t.Fatal("grouped provider canonical connect reached daemon")
	}
	combined := out.String() + err.Error()
	for _, want := range []string{"Proxy only", "podlaz debug proxy"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("missing %q: %q", want, combined)
		}
	}
	assertGroupedCLINoSensitiveMaterial(t, combined)
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
