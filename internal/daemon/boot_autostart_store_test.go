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
	testBootConfigured = "00000000-0000-0000-0000-000000000001"
	testBootAttempt    = "00000000-0000-0000-0000-000000000002"
	testBootNext       = "00000000-0000-0000-0000-000000000003"
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
	store := newBootAutostartManifestStore(t.TempDir(), fixedBootID(testBootConfigured))
	config := testBootAutostartConfig()

	manifest, err := store.Enable(config)
	if err != nil {
		t.Fatalf("Enable() error = %v", err)
	}
	if manifest.ConfiguredBootID != testBootConfigured || manifest.Generation == "" {
		t.Fatalf("unexpected manifest identity: %+v", manifest)
	}
	if !reflect.DeepEqual(manifest.Configuration, config) {
		t.Fatalf("Configuration = %+v, want %+v", manifest.Configuration, config)
	}
	info, err := os.Stat(store.path())
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("manifest mode = %o, want 600", got)
	}
	loaded, exists, err := store.Load()
	if err != nil || !exists || !reflect.DeepEqual(loaded, manifest) {
		t.Fatalf("Load() = (%+v, %v, %v), want manifest, true, nil", loaded, exists, err)
	}
}

func TestBootAutostartManifestReplacementChangesOpaqueGeneration(t *testing.T) {
	store := newBootAutostartManifestStore(t.TempDir(), fixedBootID(testBootConfigured))
	first, err := store.Enable(testBootAutostartConfig())
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Enable(testBootAutostartConfig())
	if err != nil {
		t.Fatal(err)
	}
	if first.Generation == second.Generation {
		t.Fatalf("generation reused across replacement: %q", first.Generation)
	}
}

func TestBootAutostartManifestDisableUsesAbsenceAsCanonicalOffState(t *testing.T) {
	store := newBootAutostartManifestStore(t.TempDir(), fixedBootID(testBootConfigured))
	if _, err := store.Enable(testBootAutostartConfig()); err != nil {
		t.Fatal(err)
	}
	if err := store.Disable(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("manifest still exists after Disable(): %v", err)
	}
	_, exists, err := store.Load()
	if err != nil || exists {
		t.Fatalf("Load() after Disable() = exists %v, err %v", exists, err)
	}
}

func TestBootAutostartManifestEligibilityStartsOnlyOnLaterBoot(t *testing.T) {
	store := newBootAutostartManifestStore(t.TempDir(), fixedBootID(testBootConfigured))
	manifest, err := store.Enable(testBootAutostartConfig())
	if err != nil {
		t.Fatal(err)
	}
	if manifest.EligibleForBoot(testBootConfigured) {
		t.Fatal("manifest configured in current boot is eligible immediately")
	}
	if !manifest.EligibleForBoot(testBootAttempt) {
		t.Fatal("manifest is not eligible on a later boot")
	}
}

func TestBootAutostartManifestLoadIsStrictAndBounded(t *testing.T) {
	t.Run("unknown field", func(t *testing.T) {
		store := newBootAutostartManifestStore(t.TempDir(), fixedBootID(testBootConfigured))
		data := `{"schema_version":"podlaz.boot-autostart-manifest.v1","generation":"00112233445566778899aabbccddeeff","configured_boot_id":"` + testBootConfigured + `","configuration":{"mode":"tun","profile":{"id":"example-profile","name":"Example VPN","server":"vpn.example.com","port":443,"protocol":"vless"}},"unexpected":true}`
		if err := os.WriteFile(store.path(), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.Load(); err == nil {
			t.Fatal("Load() accepted unknown field")
		}
	})

	t.Run("trailing data", func(t *testing.T) {
		store := newBootAutostartManifestStore(t.TempDir(), fixedBootID(testBootConfigured))
		data := `{"schema_version":"podlaz.boot-autostart-manifest.v1","generation":"00112233445566778899aabbccddeeff","configured_boot_id":"` + testBootConfigured + `","configuration":{"mode":"tun","profile":{"id":"example-profile","name":"Example VPN","server":"vpn.example.com","port":443,"protocol":"vless"}}}{}`
		if err := os.WriteFile(store.path(), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.Load(); err == nil {
			t.Fatal("Load() accepted trailing data")
		}
	})

	t.Run("insecure permissions", func(t *testing.T) {
		store := newBootAutostartManifestStore(t.TempDir(), fixedBootID(testBootConfigured))
		if _, err := store.Enable(testBootAutostartConfig()); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(store.path(), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.Load(); err == nil {
			t.Fatal("Load() accepted mode 0644")
		}
	})

	t.Run("oversized", func(t *testing.T) {
		store := newBootAutostartManifestStore(t.TempDir(), fixedBootID(testBootConfigured))
		manifest, err := store.Enable(testBootAutostartConfig())
		if err != nil {
			t.Fatal(err)
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
			t.Fatal("Load() accepted oversized state")
		}
	})
}

func TestBootAutostartAttemptAdmissionPinsConfigurationAndIsSingle(t *testing.T) {
	manifestStore := newBootAutostartManifestStore(t.TempDir(), fixedBootID(testBootConfigured))
	manifest, err := manifestStore.Enable(testBootAutostartConfig())
	if err != nil {
		t.Fatal(err)
	}
	store := newBootAutostartAttemptStore(t.TempDir(), fixedBootID(testBootAttempt))
	attempt, err := store.Admit(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.BootID != testBootAttempt || attempt.ManifestGeneration != manifest.Generation || attempt.State != bootAutostartAttemptInProgress {
		t.Fatalf("unexpected attempt: %+v", attempt)
	}
	if !reflect.DeepEqual(attempt.Configuration, manifest.Configuration) {
		t.Fatalf("attempt configuration was not pinned: %+v", attempt.Configuration)
	}
	if _, err := store.Admit(manifest); !errors.Is(err, errBootAutostartAttemptAlreadyAdmitted) {
		t.Fatalf("second Admit() error = %v", err)
	}

	changed := testBootAutostartConfig()
	changed.Profile.ID = "replacement-example-profile"
	changed.Profile.Name = "Replacement Example VPN"
	if _, err := manifestStore.Enable(changed); err != nil {
		t.Fatal(err)
	}
	loaded, exists, err := store.LoadCurrent()
	if err != nil || !exists || !reflect.DeepEqual(loaded.Configuration, attempt.Configuration) {
		t.Fatalf("pinned attempt changed after manifest replacement: %+v, exists=%v, err=%v", loaded, exists, err)
	}
}

func TestBootAutostartAttemptLoadDiscardsPreviousBootRecord(t *testing.T) {
	manifestStore := newBootAutostartManifestStore(t.TempDir(), fixedBootID(testBootConfigured))
	manifest, err := manifestStore.Enable(testBootAutostartConfig())
	if err != nil {
		t.Fatal(err)
	}
	runtimeDir := t.TempDir()
	attemptBootStore := newBootAutostartAttemptStore(runtimeDir, fixedBootID(testBootAttempt))
	if _, err := attemptBootStore.Admit(manifest); err != nil {
		t.Fatal(err)
	}

	nextBootStore := newBootAutostartAttemptStore(runtimeDir, fixedBootID(testBootNext))
	_, exists, err := nextBootStore.LoadCurrent()
	if err != nil || exists {
		t.Fatalf("LoadCurrent() on next boot = exists %v, err %v", exists, err)
	}
	if _, err := os.Stat(nextBootStore.path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("previous-boot attempt was not removed: %v", err)
	}
}

func TestBootAutostartAttemptCompletionIsOneWayAndIdempotent(t *testing.T) {
	manifestStore := newBootAutostartManifestStore(t.TempDir(), fixedBootID(testBootConfigured))
	manifest, err := manifestStore.Enable(testBootAutostartConfig())
	if err != nil {
		t.Fatal(err)
	}

	t.Run("succeeded", func(t *testing.T) {
		store := newBootAutostartAttemptStore(t.TempDir(), fixedBootID(testBootAttempt))
		if _, err := store.Admit(manifest); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkSucceeded(); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkSucceeded(); err != nil {
			t.Fatalf("idempotent MarkSucceeded() error = %v", err)
		}
		if err := store.MarkTerminal(bootAutostartTerminalConnectFailed); err == nil {
			t.Fatal("succeeded attempt transitioned to terminal")
		}
	})

	t.Run("terminal", func(t *testing.T) {
		store := newBootAutostartAttemptStore(t.TempDir(), fixedBootID(testBootAttempt))
		if _, err := store.Admit(manifest); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkTerminal(bootAutostartTerminalConnectFailed); err != nil {
			t.Fatal(err)
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
	store := newBootAutostartAttemptStore(runtimeDir, fixedBootID(testBootAttempt))
	if err := os.WriteFile(filepath.Join(runtimeDir, bootAutostartAttemptFileName), []byte(`{"schema_version":"broken"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.LoadCurrent(); err == nil {
		t.Fatal("LoadCurrent() accepted ambiguous current attempt state")
	}
}
