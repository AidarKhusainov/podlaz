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

func TestPreinstallMarksConfigFilesInstallForStaleHelperRepair(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("sh", "preinstall.txt", "install", "0.0.0~dev-1", "0.0.0~dev-1")
	cmd.Env = append(os.Environ(), "PODLAZ_MAINTSCRIPT_RUN_DIR="+dir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("preinstall failed: %v\n%s", err, output)
	}

	if _, err := os.Stat(filepath.Join(dir, "repair-helper-enable")); err != nil {
		t.Fatalf("expected stale helper repair marker: %v", err)
	}
}

func TestPreinstallDoesNotMarkFreshInstall(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("sh", "preinstall.txt", "install")
	cmd.Env = append(os.Environ(), "PODLAZ_MAINTSCRIPT_RUN_DIR="+dir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("preinstall failed: %v\n%s", err, output)
	}

	if _, err := os.Stat(filepath.Join(dir, "repair-helper-enable")); !os.IsNotExist(err) {
		t.Fatalf("fresh install should not create stale helper repair marker: %v", err)
	}
}

func TestPostinstallRepairsConfigFilesStaleHelperEnablement(t *testing.T) {
	h := newPostinstallHarness(t, postinstallOptions{
		marker:           true,
		initiallyEnabled: false,
		debianInstalled:  true,
		wasEnabled:       true,
	})

	log := h.runPostinstall(t)

	assertLogContains(t, log,
		"systemd-sysusers /usr/lib/sysusers.d/podlaz.conf",
		"deb-systemd-helper unmask podlazd.service",
		"deb-systemd-helper enable podlazd.service",
		"deb-systemd-helper update-state podlazd.service",
		"systemctl is-enabled --quiet podlazd.service",
		"deb-systemd-helper debian-installed podlazd.service",
		"deb-systemd-helper was-enabled podlazd.service",
		"deb-systemd-helper reenable podlazd.service",
		"deb-systemd-invoke start podlazd.service",
	)
	assertLogContainsInOrder(t, log,
		"deb-systemd-helper reenable podlazd.service",
		"deb-systemd-helper update-state podlazd.service",
		"deb-systemd-invoke start podlazd.service",
	)

	if _, err := os.Stat(filepath.Join(h.runDir, "repair-helper-enable")); !os.IsNotExist(err) {
		t.Fatalf("postinstall should remove stale helper repair marker: %v", err)
	}
}

func TestPostinstallDoesNotRepairOrStartAdminDisabledInstalledUnit(t *testing.T) {
	h := newPostinstallHarness(t, postinstallOptions{
		marker:           false,
		initiallyEnabled: false,
		debianInstalled:  true,
		wasEnabled:       true,
	})

	log := h.runPostinstall(t)

	assertLogNotContains(t, log,
		"deb-systemd-helper debian-installed podlazd.service",
		"deb-systemd-helper was-enabled podlazd.service",
		"deb-systemd-helper reenable podlazd.service",
		"deb-systemd-invoke start podlazd.service",
	)
	assertLogContains(t, log, "systemctl is-enabled --quiet podlazd.service")
}

func TestPostinstallDoesNotReenableAlreadyEnabledUnit(t *testing.T) {
	h := newPostinstallHarness(t, postinstallOptions{
		marker:           true,
		initiallyEnabled: true,
		debianInstalled:  true,
		wasEnabled:       true,
	})

	log := h.runPostinstall(t)

	assertLogNotContains(t, log,
		"deb-systemd-helper debian-installed podlazd.service",
		"deb-systemd-helper was-enabled podlazd.service",
		"deb-systemd-helper reenable podlazd.service",
	)
	assertLogContains(t, log,
		"systemctl is-enabled --quiet podlazd.service",
		"deb-systemd-invoke start podlazd.service",
	)
}

func TestMaintainerScriptsAvoidRawSystemctlServiceMutation(t *testing.T) {
	for _, script := range []string{"preinstall.txt", "postinstall", "preremove", "postremove"} {
		content, err := os.ReadFile(script)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("read %s: %v", script, err)
		}

		re := regexp.MustCompile(`systemctl([[:space:]]+--[[:alnum:]-]+)*[[:space:]]+(start|enable)\b`)
		if re.Match(content) {
			t.Fatalf("%s must use Debian helper tools instead of raw systemctl mutation", script)
		}
	}
}

type postinstallOptions struct {
	marker           bool
	initiallyEnabled bool
	debianInstalled  bool
	wasEnabled       bool
}

type postinstallHarness struct {
	binDir string
	log    string
	runDir string
}

func newPostinstallHarness(t *testing.T, opts postinstallOptions) postinstallHarness {
	t.Helper()

	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatalf("create stub bin dir: %v", err)
	}
	runDir := filepath.Join(dir, "run")
	if err := os.Mkdir(runDir, 0o755); err != nil {
		t.Fatalf("create run dir: %v", err)
	}
	if opts.marker {
		if err := os.WriteFile(filepath.Join(runDir, "repair-helper-enable"), []byte{}, 0o600); err != nil {
			t.Fatalf("write marker: %v", err)
		}
	}

	logPath := filepath.Join(dir, "calls.log")
	enabledPath := filepath.Join(dir, "enabled")
	if opts.initiallyEnabled {
		if err := os.WriteFile(enabledPath, []byte{}, 0o600); err != nil {
			t.Fatalf("write enabled flag: %v", err)
		}
	}

	writeStub(t, binDir, "systemd-sysusers", fmt.Sprintf(`#!/bin/sh
printf 'systemd-sysusers %%s\n' "$*" >> %q
exit 0
`, logPath))
	writeStub(t, binDir, "deb-systemd-invoke", fmt.Sprintf(`#!/bin/sh
printf 'deb-systemd-invoke %%s\n' "$*" >> %q
exit 0
`, logPath))
	writeStub(t, binDir, "systemctl", fmt.Sprintf(`#!/bin/sh
printf 'systemctl %%s\n' "$*" >> %q
if [ "$1" = is-enabled ]; then
  test -e %q
  exit $?
fi
exit 0
`, logPath, enabledPath))

	debianInstalledExit := 1
	if opts.debianInstalled {
		debianInstalledExit = 0
	}
	wasEnabledExit := 1
	if opts.wasEnabled {
		wasEnabledExit = 0
	}
	writeStub(t, binDir, "deb-systemd-helper", fmt.Sprintf(`#!/bin/sh
printf 'deb-systemd-helper %%s\n' "$*" >> %q
case "$1" in
  debian-installed)
    exit %d
    ;;
  was-enabled)
    exit %d
    ;;
  reenable)
    : > %q
    exit 0
    ;;
esac
exit 0
`, logPath, debianInstalledExit, wasEnabledExit, enabledPath))

	return postinstallHarness{
		binDir: binDir,
		log:    logPath,
		runDir: runDir,
	}
}

func (h postinstallHarness) runPostinstall(t *testing.T) string {
	t.Helper()

	cmd := exec.Command("sh", "postinstall", "configure")
	cmd.Env = append(os.Environ(),
		"PATH="+h.binDir+":"+os.Getenv("PATH"),
		"PODLAZ_MAINTSCRIPT_RUN_DIR="+h.runDir,
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

func assertLogNotContains(t *testing.T, log string, unexpected ...string) {
	t.Helper()
	for _, deny := range unexpected {
		if strings.Contains(log, deny) {
			t.Fatalf("stub log unexpectedly contains %q; log:\n%s", deny, log)
		}
	}
}

func assertLogContainsInOrder(t *testing.T, log string, expected ...string) {
	t.Helper()
	pos := 0
	for _, want := range expected {
		next := strings.Index(log[pos:], want)
		if next < 0 {
			t.Fatalf("stub log missing %q after offset %d; log:\n%s", want, pos, log)
		}
		pos += next + len(want)
	}
}
