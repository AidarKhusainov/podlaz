package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/testfixtures"
)

const groupedCLIProfileID = "xray-json-redaction"

func TestRunCLIPlanTunRejectsGroupedProviderBeforeSnapshot(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{
		profileStorePath: storePath,
		systemSnapshot: func(ctx context.Context, opts netsnapshot.Options) netsnapshot.Snapshot {
			t.Fatal("TUN plan for grouped provider profiles must fail before collecting host networking snapshot")
			return netsnapshot.Snapshot{}
		},
	}
	addGroupedCLIProfile(t, opts)

	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"plan", "--mode", "tun", groupedCLIProfileID}, &out, opts)
	if err == nil {
		t.Fatal("expected grouped provider TUN plan to fail")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("expected usage exit code 2, got %d", got)
	}
	combined := out.String() + err.Error()
	if !strings.Contains(combined, "TUN-mode grouped Xray profiles are not supported") {
		t.Fatalf("expected grouped TUN unsupported diagnostic, got output=%q err=%v", out.String(), err)
	}
	assertGroupedCLINoSensitiveMaterial(t, combined)
}

func TestRunCLIGroupedProviderProfileOutputRedaction(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{profileStorePath: storePath}
	addGroupedCLIProfile(t, opts)

	commands := [][]string{
		{"profile", "list"},
		{"profile", "list", "--json"},
		{"profile", "show", groupedCLIProfileID},
		{"profile", "show", groupedCLIProfileID, "--json"},
		{"profile", "validate", groupedCLIProfileID, "--mode", "proxy-only"},
		{"profile", "validate", groupedCLIProfileID, "--mode", "proxy-only", "--json"},
	}
	for _, args := range commands {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var out bytes.Buffer
			if err := runWithOptions(context.Background(), args, &out, opts); err != nil {
				t.Fatalf("%v failed: %v", args, err)
			}
			assertGroupedCLINoSensitiveMaterial(t, out.String())
		})
	}
}

func TestRunCLIGroupedProviderValidationErrorRedaction(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{profileStorePath: storePath}
	addGroupedCLIProfile(t, opts)

	commands := [][]string{
		{"profile", "validate", groupedCLIProfileID, "--mode", "tun"},
		{"profile", "validate", groupedCLIProfileID, "--mode", "tun", "--json"},
	}
	for _, args := range commands {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var out bytes.Buffer
			err := runWithOptions(context.Background(), args, &out, opts)
			if err == nil {
				t.Fatalf("expected %v to fail", args)
			}
			if got := ExitCode(err); got != 3 {
				t.Fatalf("expected diagnostic exit code 3, got %d", got)
			}
			combined := out.String() + err.Error()
			if !strings.Contains(combined, "TUN-mode grouped Xray profiles are not supported") {
				t.Fatalf("expected grouped TUN unsupported diagnostic, got output=%q err=%v", out.String(), err)
			}
			assertGroupedCLINoSensitiveMaterial(t, combined)
		})
	}
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
