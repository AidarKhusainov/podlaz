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

	cmd := exec.Command("sh", "postinstall", "configure")
	cmd.Env = append(os.Environ(),
		"PATH="+h.binDir+":"+os.Getenv("PATH"),
		"PODLAZ_MAINTSCRIPT_RUN_DIR="+h.runDir,
		"PODLAZ_RUNTIME_DIR="+runtimeDir,
		"PODLAZ_BOOT_ID_PATH="+bootIDPath,
		"PODLAZ_SYSTEMD_RUNTIME_DIR="+systemdRuntimeDir,
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("postinstall accepted unknown pre-continuation service contract; output:\n%s", output)
	}

	logBytes, readErr := os.ReadFile(h.log)
	if readErr != nil {
		t.Fatalf("read stub call log: %v", readErr)
	}
	log := string(logBytes)
	if strings.Contains(log, "deb-systemd-invoke try-restart podlazd.service") || strings.Contains(log, "deb-systemd-invoke start podlazd.service") {
		t.Fatalf("unknown service contract must fail before process mutation; log:\n%s", log)
	}
	if _, statErr := os.Stat(filepath.Join(runtimeDir, "legacy-upgrade-continuation")); !os.IsNotExist(statErr) {
		t.Fatalf("unknown service contract must not mint legacy migration authority: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(systemdRuntimeDir, "podlazd.service.d", "50-podlaz-legacy-replacement.conf")); !os.IsNotExist(statErr) {
		t.Fatalf("unknown service contract must not install legacy replacement override: %v", statErr)
	}
}
