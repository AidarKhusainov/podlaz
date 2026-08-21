package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

const (
	testBootOne = "00000000-0000-0000-0000-000000000001"
	testBootTwo = "00000000-0000-0000-0000-000000000002"
)

func testBootAutostartConfig() api.AutostartConfigureRequest {
	return api.AutostartConfigureRequest{
		Mode: "tun",
		Profile: api.ProfileSnapshot{
			ID:           "example-profile",
			Name:         "Example VPN",
			Source:       "manual",
			Engine:       "xray",
			Server:       "vpn.example.com",
			Port:         443,
			Protocol:     "vless",
			UserIdentity: "11111111-1111-1111-1111-111111111111",
			Transport:    "tcp",
			Security:     "tls",
			Encryption:   "none",
		},
	}
}

func TestBootAutostartManifestEnableRoundTripsPrivateValidatedConfig(t *testing.T) {
	stateDir := t.TempDir()
	store := newBootAutostartManifestStore(stateDir, fixedBootID(testBootOne))
	config := testBootAutostartConfig()

	manifest, err := store.Enable(config)
	if err != nil {
		t.Fatalf("Enable() error = %v", err)
	}
	if manifest.ConfiguredBootID != testBootOne {
		t.Fatalf("ConfiguredBootID = %q, want %q", manifest.ConfiguredBootID, testBootOne)
	}
	if manifest.Generation == "" {
		t.Fatal("Generation is empty")
	}
	if !reflect.DeepEqual(manifest.Configuration, config) {
		t.Fatalf("Configuration = %+v, want %+v", manifest.Configuration, config)
	}

	info, err := os.Stat(store.path())
	if err != nil {
		t.Fatalf("stat manifest: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("manifest mode = %o, want 600", got)
	}

	loaded, exists, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !exists || !reflect.DeepEqual(loaded, manifest) {
		t.Fatalf("Load() = (%+v, %v), want (%+v, true)", loaded, exists, manifest)
	}
}

func TestBootAutostartManifestEnableChangesOpaqueGeneration(t *testing.T) {
	store := newBootAutostartManifestStore(t.TempDir(), fixedBootID(testBootOne))
	first, err := store.Enable(testBootAutostartConfig())
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Enable(testBootAutostartConfig())
	if err != nil {
		t.Fatal(err)
	}
	if first.Generation == second.Generation {
		t.Fatalf("generation was reused across manifest replacement: %q", first.Generation)
	}
}

func TestBootAutostartManifestDisableUsesAbsenceAsCanonicalOffState(t *testing.T) {
	stateDir := t.TempDir()
	store := newBootAutostartManifestStore(stateDir, fixedBootID(testBootOne))
	if _, err := store.Enable(testBootAutostartConfig()); err != nil {
		t.Fatal(err)
	}
	if err := store.Disable(); err != nil {
		t.Fatalf("Disable() error = %v", err)
	}
	if _, err := os.Stat(store.path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("manifest still exists after Disable(): %v", err)
	}
	_, exists, err := store.Load()
	if err != nil || exists {
		t.Fatalf("Load() after Disable() = exists %v, err %v; want false, nil", exists, err)
	}
}

func TestBootAutostartManifestLoadRejectsUnknownAndTrailingData(t *testing.T) {
	for name, data := range map[string]string{
		"unknown field": `{"schema_version":"podlaz.boot-autostart-manifest.v1","generation":"00112233445566778899aabbccddeeff","configured_boot_id":"` + testBootOne + `","configuration":{"mode":"tun","profile":{"id":"example-profile","name":"Example VPN","server":"vpn.example.com","port":443,"protocol":"vless"}},"unexpected":true}`,
		"trailing data": `{"schema_version":"podlaz.boot-autostart-manifest.v1","generation":"00112233445566778899aabbccddeeff","configured_boot_id":"` + testBootOne + `","configuration":{"mode":"tun","profile":{"id":"example-profile","name":"Example VPN","server":"vpn.example.com","port":443,"protocol":"vless"}}}{}`,
	} {
		t.Run(name, func(t *testing.T) {
			stateDir := t.TempDir()
			store := newBootAutostartManifestStore(stateDir, fixedBootID(testBootOne))
			if err := os.WriteFile(store.path(), []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := store.Load(); err == nil {
				t.Fatal("Load() error = nil, want strict decode failure")
			}
		})
	}
}

func TestBootAutostartManifestLoadRejectsInsecurePermissionsAndOversize(t *testing.T) {
	stateDir := t.TempDir()
	store := newBootAutostartManifestStore(stateDir, fixedBootID(testBootOne))
	manifest, err := store.Enable(testBootAutostartConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(store.path(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Load(); err == nil {
		t.Fatal("Load() accepted mode 0644 manifest")
	}

	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	oversized := append(encoded, make([]byte, maxBootAutostartStateBytes+1)...)
	if err := os.WriteFile(store.path(), oversized, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Load(); err == nil {
		t.Fatal("Load() accepted oversized manifest")
	}
}

func TestBootAutostartManifestEligibilityStartsOnlyOnLaterBoot(t *testing.T) {
	store := newBootAutostartManifestStore(t.TempDir(), fixedBootID(testBootOne))
	manifest, err := store.Enable(testBootAutostartConfig())
	if err != nil {
		t.Fatal(err)
	}
	if manifest.EligibleForBoot(testBootOne) {
		t.Fatal("manifest configured in current boot is eligible immediately")
	}
	if !manifest.EligibleForBoot(testBootTwo) {
		t.Fatal("manifest is not eligible on a later boot")
	}
}

func TestBootAutostartAttemptAdmissionPinsExactConfiguration(t *testing.T) {
	runtimeDir := t.TempDir()
	manifestStore := newBootAutostartManifestStore(t.TempDir(), fixedBootID(testBootOne))
	manifest, err := manifestStore.Enable(testBootAutostartConfig())
	if err != nil {
		t.Fatal(err)
	}
	attemptStore := newBootAutostartAttemptStore(runtimeDir, fixedBootID(testBootTwo))

	attempt, err := attemptStore.Admit(manifest)
	if err != nil {
		t.Fatalf("Admit() error = %v", err)
	}
	if attempt.BootID != testBootTwo || attempt.ManifestGeneration != manifest.Generation || attempt.State != bootAutostartAttemptInProgress {
		t.Fatalf("unexpected admitted attempt: %+v", attempt)
	}
	if !reflect.DeepEqual(attempt.Configuration, manifest.Configuration) {
		t.Fatalf("attempt configuration = %+v, want pinned %+v", attempt.Configuration, manifest.Configuration)
	}

	changed := testBootAutostartConfig()
	changed.Profile.ID = "replacement-example-profile"
	changed.Profile.Name = "Replacement Example VPN"
	if _, err := manifestStore.Enable(changed); err != nil {
		t.Fatal(err)
	}
	loaded, exists, err := attemptStore.LoadCurrent()
	if err != nil || !exists {
		t.Fatalf("LoadCurrent() = exists %v, err %v", exists, err)
	}
	if !reflect.DeepEqual(loaded.Configuration, attempt.Configuration) {
		t.Fatalf("in-progress configuration changed after manifest replacement: %+v", loaded.Configuration)
	}
}

func TestBootAutostartAttemptAdmissionIsSingleForCurrentBoot(t *testing.T) {
	manifestStore := newBootAutostartManifestStore(t.TempDir(), fixedBootID(testBootOne))
	manifest, err := manifestStore.Enable(testBootAutostartConfig())
	if err != nil {
		t.Fatal(err)
	}
	attemptStore := newBootAutostartAttemptStore(t.TempDir(), fixedBootID(testBootTwo))
	if _, err := attemptStore.Admit(manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := attemptStore.Admit(manifest); !errors.Is(err, errBootAutostartAttemptAlreadyAdmitted) {
		t.Fatalf("second Admit() error = %v, want errBootAutostartAttemptAlreadyAdmitted", err)
	}
}

func TestBootAutostartAttemptLoadDiscardsPreviousBootRecord(t *testing.T) {
	manifestStore := newBootAutostartManifestStore(t.TempDir(), fixedBootID(testBootOne))
	manifest, err := manifestStore.Enable(testBootAutostartConfig())
	if err != nil {
		t.Fatal(err)
	}
	runtimeDir := t.TempDir()
	firstBootStore := newBootAutostartAttemptStore(runtimeDir, fixedBootID(testBootOne))
	if _, err := firstBootStore.Admit(manifest); err != nil {
		t.Fatal(err)
	}

	secondBootStore := newBootAutostartAttemptStore(runtimeDir, fixedBootID(testBootTwo))
	_, exists, err := secondBootStore.LoadCurrent()
	if err != nil || exists {
		t.Fatalf("LoadCurrent() on later boot = exists %v, err %v; want false, nil", exists, err)
	}
	if _, err := os.Stat(secondBootStore.path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("previous-boot attempt was not removed: %v", err)
	}
}

func TestBootAutostartAttemptCompletionIsOneWayAndIdempotent(t *testing.T) {
	manifestStore := newBootAutostartManifestStore(t.TempDir(), fixedBootID(testBootOne))
	manifest, err := manifestStore.Enable(testBootAutostartConfig())
	if err != nil {
		t.Fatal(err)
	}

	t.Run("succeeded", func(t *testing.T) {
		store := newBootAutostartAttemptStore(t.TempDir(), fixedBootID(testBootTwo))
		if _, err := store.Admit(manifest); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkSucceeded(); err != nil {
			t.Fatalf("MarkSucceeded() error = %v", err)
		}
		if err := store.MarkSucceeded(); err != nil {
			t.Fatalf("idempotent MarkSucceeded() error = %v", err)
		}
		if err := store.MarkTerminal(bootAutostartTerminalConnectFailed); err == nil {
			t.Fatal("succeeded attempt transitioned to terminal")
		}
	})

	t.Run("terminal", func(t *testing.T) {
		store := newBootAutostartAttemptStore(t.TempDir(), fixedBootID(testBootTwo))
		if _, err := store.Admit(manifest); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkTerminal(bootAutostartTerminalConnectFailed); err != nil {
			t.Fatalf("MarkTerminal() error = %v", err)
		}
		if err := store.MarkTerminal(bootAutostartTerminalConnectFailed); err != nil {
			t.Fatalf("idempotent MarkTerminal() error = %v", err)
		}
		if err := store.MarkSucceeded(); err == nil {
			t.Fatal("terminal attempt transitioned to succeeded")
		}
	})
}

func TestBootAutostartAttemptCorruptionFailsClosed(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newBootAutostartAttemptStore(runtimeDir, fixedBootID(testBootOne))
	if err := os.WriteFile(filepath.Join(runtimeDir, bootAutostartAttemptFileName), []byte(`{"schema_version":"broken"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.LoadCurrent(); err == nil {
		t.Fatal("LoadCurrent() accepted ambiguous current attempt state")
	}
}
