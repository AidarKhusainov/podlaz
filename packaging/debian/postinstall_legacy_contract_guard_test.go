package debian

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPostinstallRejectsUnknownPreContinuationServiceContractBeforeReplacement(t *testing.T) {
	h := newPostinstallHarness(t, postinstallOptions{
		initiallyEnabled:    true,
		loadedRestartSignal: "12",
	})
	runtimeDir := filepath.Join(t.TempDir(), "podlaz-runtime")
	if err := os.Mkdir(runtimeDir, 0o711); err != nil {
		t.Fatal(err)
	}
	bootIDPath := filepath.Join(t.TempDir(), "boot-id")
	if err := os.WriteFile(bootIDPath, []byte("boot-example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	systemdRuntimeDir := filepath.Join(t.TempDir(), "systemd-run")
	if err := os.Mkdir(systemdRuntimeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	output, err := runPostinstallGuardCase(t, h, runtimeDir, bootIDPath, systemdRuntimeDir)
	if err == nil {
		t.Fatalf("postinstall accepted unknown pre-continuation service contract; output:\n%s", output)
	}

	log := readPostinstallGuardLog(t, h)
	assertNoPostinstallProcessMutation(t, log)
	if _, statErr := os.Stat(filepath.Join(runtimeDir, "legacy-upgrade-continuation")); !os.IsNotExist(statErr) {
		t.Fatalf("unknown service contract must not mint legacy migration authority: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(systemdRuntimeDir, "podlazd.service.d", "50-podlaz-legacy-replacement.conf")); !os.IsNotExist(statErr) {
		t.Fatalf("unknown service contract must not install legacy replacement override: %v", statErr)
	}
}

func TestPostinstallDoesNotRestartWhileLegacyMigrationAuthorityIsUnresolved(t *testing.T) {
	h := newPostinstallHarness(t, postinstallOptions{initiallyEnabled: true})
	runtimeDir := filepath.Join(t.TempDir(), "podlaz-runtime")
	if err := os.Mkdir(runtimeDir, 0o711); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "legacy-upgrade-continuation"), []byte("boot-example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bootIDPath := filepath.Join(t.TempDir(), "boot-id")
	if err := os.WriteFile(bootIDPath, []byte("boot-example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	systemdRuntimeDir := filepath.Join(t.TempDir(), "systemd-run")
	if err := os.Mkdir(systemdRuntimeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	output, err := runPostinstallGuardCase(t, h, runtimeDir, bootIDPath, systemdRuntimeDir)
	if err == nil {
		t.Fatalf("postinstall restarted while legacy migration authority was unresolved; output:\n%s", output)
	}
	assertNoPostinstallProcessMutation(t, readPostinstallGuardLog(t, h))
	if _, statErr := os.Stat(filepath.Join(runtimeDir, "legacy-upgrade-continuation")); statErr != nil {
		t.Fatalf("unresolved legacy authority must remain durable: %v", statErr)
	}
}

func runPostinstallGuardCase(t *testing.T, h postinstallHarness, runtimeDir, bootIDPath, systemdRuntimeDir string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command("sh", "postinstall", "configure")
	cmd.Env = append(os.Environ(),
		"PATH="+h.binDir+":"+os.Getenv("PATH"),
		"PODLAZ_MAINTSCRIPT_RUN_DIR="+h.runDir,
		"PODLAZ_RUNTIME_DIR="+runtimeDir,
		"PODLAZ_BOOT_ID_PATH="+bootIDPath,
		"PODLAZ_SYSTEMD_RUNTIME_DIR="+systemdRuntimeDir,
	)
	return cmd.CombinedOutput()
}

func readPostinstallGuardLog(t *testing.T, h postinstallHarness) string {
	t.Helper()
	logBytes, err := os.ReadFile(h.log)
	if err != nil {
		t.Fatalf("read stub call log: %v", err)
	}
	return string(logBytes)
}

func assertNoPostinstallProcessMutation(t *testing.T, log string) {
	t.Helper()
	if strings.Contains(log, "deb-systemd-invoke try-restart podlazd.service") || strings.Contains(log, "deb-systemd-invoke start podlazd.service") {
		t.Fatalf("guarded service state must fail before process mutation; log:\n%s", log)
	}
}
