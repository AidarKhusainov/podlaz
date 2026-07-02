package debian

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestPostinstallRepairsStaleHelperEnablement(t *testing.T) {
	log := runPostinstallWithSystemdEnablement(t, 1)

	assertLogContains(t, log,
		"systemd-sysusers /usr/lib/sysusers.d/podlaz.conf",
		"deb-systemd-helper unmask podlazd.service",
		"deb-systemd-helper enable podlazd.service",
		"deb-systemd-helper update-state podlazd.service",
		"systemctl is-enabled --quiet podlazd.service",
		"deb-systemd-helper reenable podlazd.service",
		"deb-systemd-invoke start podlazd.service",
	)
}

func TestPostinstallDoesNotReenableAlreadyEnabledUnit(t *testing.T) {
	log := runPostinstallWithSystemdEnablement(t, 0)

	if strings.Contains(log, "deb-systemd-helper reenable podlazd.service") {
		t.Fatalf("postinstall unexpectedly reenabled an already-enabled unit; log:\n%s", log)
	}
	assertLogContains(t, log,
		"systemctl is-enabled --quiet podlazd.service",
		"deb-systemd-invoke start podlazd.service",
	)
}

func TestPostinstallAvoidsRawSystemctlServiceMutation(t *testing.T) {
	content, err := os.ReadFile("postinstall")
	if err != nil {
		t.Fatalf("read postinstall: %v", err)
	}

	for _, pattern := range []string{
		`systemctl[[:space:]]+start[[:space:]]+podlazd`,
		`systemctl[[:space:]]+enable[[:space:]]+podlazd`,
	} {
		re := regexp.MustCompile(pattern)
		if re.Match(content) {
			t.Fatalf("postinstall must use Debian helper tools instead of raw systemctl mutation; matched %q", pattern)
		}
	}
}

func runPostinstallWithSystemdEnablement(t *testing.T, exitCode int) string {
	t.Helper()

	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatalf("create stub bin dir: %v", err)
	}
	logPath := filepath.Join(dir, "calls.log")

	writeStub(t, binDir, "systemd-sysusers", fmt.Sprintf(`#!/bin/sh
printf 'systemd-sysusers %%s\n' "$*" >> %q
exit 0
`, logPath))
	writeStub(t, binDir, "deb-systemd-helper", fmt.Sprintf(`#!/bin/sh
printf 'deb-systemd-helper %%s\n' "$*" >> %q
exit 0
`, logPath))
	writeStub(t, binDir, "deb-systemd-invoke", fmt.Sprintf(`#!/bin/sh
printf 'deb-systemd-invoke %%s\n' "$*" >> %q
exit 0
`, logPath))
	writeStub(t, binDir, "systemctl", fmt.Sprintf(`#!/bin/sh
printf 'systemctl %%s\n' "$*" >> %q
if [ "$1" = is-enabled ]; then
  exit %d
fi
exit 0
`, logPath, exitCode))

	cmd := exec.Command("sh", "postinstall", "configure")
	cmd.Env = append(os.Environ(), "PATH="+binDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("postinstall failed: %v\n%s", err, output)
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read stub call log: %v", err)
	}
	return string(logBytes)
}

func writeStub(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write %s stub: %v", name, err)
	}
}

func assertLogContains(t *testing.T, log string, expected ...string) {
	t.Helper()
	for _, want := range expected {
		if !strings.Contains(log, want) {
			t.Fatalf("stub log missing %q; log:\n%s", want, log)
		}
	}
}
