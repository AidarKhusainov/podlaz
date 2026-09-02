package debian

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPostinstallMarksCurrentBootBeforeRestartWhenLegacyDaemonIsActive(t *testing.T) {
	h := newPostinstallHarness(t, postinstallOptions{
		initiallyEnabled:    true,
		loadedRestartSignal: "SIGTERM",
	})
	runtimeDir := filepath.Join(t.TempDir(), "podlaz-runtime")
	if err := os.Mkdir(runtimeDir, 0o711); err != nil {
		t.Fatal(err)
	}
	bootIDPath := filepath.Join(t.TempDir(), "boot-id")
	if err := os.WriteFile(bootIDPath, []byte("boot-example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	systemdRuntimeDir := filepath.Join(t.TempDir(), "systemd")
	if err := os.Mkdir(systemdRuntimeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	log := runPostinstallWithLifecyclePaths(t, h, runtimeDir, bootIDPath, systemdRuntimeDir)

	markerPath := filepath.Join(runtimeDir, "legacy-upgrade-continuation")
	marker, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("read legacy upgrade marker: %v", err)
	}
	if string(marker) != "boot-example\n" {
		t.Fatalf("legacy upgrade marker = %q", marker)
	}
	info, err := os.Stat(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("legacy upgrade marker mode = %o, want 600", info.Mode().Perm())
	}
	assertLogContainsInOrder(t, log,
		"systemctl is-active --quiet podlazd.service",
		"systemctl show --property=RestartKillSignal --value podlazd.service",
		"systemctl daemon-reload",
		"deb-systemd-invoke try-restart podlazd.service",
	)
}

func TestPostinstallDoesNotCreateLegacyMarkerWhenContinuationAlreadyExists(t *testing.T) {
	h := newPostinstallHarness(t, postinstallOptions{initiallyEnabled: true})
	runtimeDir := filepath.Join(t.TempDir(), "podlaz-runtime")
	if err := os.Mkdir(runtimeDir, 0o711); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "network-session-continuation.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bootIDPath := filepath.Join(t.TempDir(), "boot-id")
	if err := os.WriteFile(bootIDPath, []byte("boot-example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	systemdRuntimeDir := filepath.Join(t.TempDir(), "systemd")
	if err := os.Mkdir(systemdRuntimeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	log := runPostinstallWithLifecyclePaths(t, h, runtimeDir, bootIDPath, systemdRuntimeDir)

	if _, err := os.Stat(filepath.Join(runtimeDir, "legacy-upgrade-continuation")); !os.IsNotExist(err) {
		t.Fatalf("existing continuation must suppress legacy marker: %v", err)
	}
	if strings.Contains(log, "systemctl show --property=RestartKillSignal") {
		t.Fatalf("existing continuation should suppress legacy restart-signal probing; log:\n%s", log)
	}
	assertLogContains(t, log, "deb-systemd-invoke try-restart podlazd.service")
}

func runPostinstallWithLifecyclePaths(t *testing.T, h postinstallHarness, runtimeDir, bootIDPath, systemdRuntimeDir string) string {
	t.Helper()
	cmd := exec.Command("sh", "postinstall", "configure")
	cmd.Env = append(os.Environ(),
		"PATH="+h.binDir+":"+os.Getenv("PATH"),
		"PODLAZ_MAINTSCRIPT_RUN_DIR="+h.runDir,
		"PODLAZ_RUNTIME_DIR="+runtimeDir,
		"PODLAZ_BOOT_ID_PATH="+bootIDPath,
		"PODLAZ_SYSTEMD_RUNTIME_DIR="+systemdRuntimeDir,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("postinstall failed: %v\n%s", err, output)
	}
	logBytes, err := os.ReadFile(h.log)
	if err != nil {
		t.Fatalf("read stub call log: %v", err)
	}
	return string(logBytes)
}
