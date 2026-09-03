package debian

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestHistoricalLegacyDaemonDoesNotExitOnCandidateRestartSignal(t *testing.T) {
	if os.Getenv("PODLAZ_LEGACY_SIGNAL_HELPER") == "1" {
		signals := make(chan os.Signal, 1)
		signalNotifyLegacy(signals)
		for {
			select {
			case <-signals:
				os.Exit(0)
			case <-time.After(time.Hour):
				os.Exit(2)
			}
		}
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestHistoricalLegacyDaemonDoesNotExitOnCandidateRestartSignal")
	cmd.Env = append(os.Environ(), "PODLAZ_LEGACY_SIGNAL_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	time.Sleep(50 * time.Millisecond)
	if err := cmd.Process.Signal(syscall.SIGUSR1); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("historical daemon unexpectedly exited on SIGUSR1: %v", err)
	}
}

func signalNotifyLegacy(ch chan<- os.Signal) {
	// v0.2.29 (c846f5465a90a50d72f3fc393d639a402d590798) registered only SIGINT/SIGTERM.
	signalNotify(ch, os.Interrupt, syscall.SIGTERM)
}

var signalNotify = func(ch chan<- os.Signal, sig ...os.Signal) { signal.Notify(ch, sig...) }

func TestPostinstallLegacyReplacementPreservesAuthorityAndUsesOneExplicitCutover(t *testing.T) {
	h := newLegacyReplacementHarness(t, "success")
	log, err := h.run(t)
	if err != nil {
		t.Fatalf("postinstall failed: %v\n%s", err, log)
	}

	marker := filepath.Join(h.runtimeDir, "legacy-upgrade-continuation")
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if string(data) != "boot-example\n" {
		t.Fatalf("marker = %q", data)
	}
	for _, path := range h.evidence {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("migration evidence %s did not survive: %v", path, err)
		}
	}
	assertLegacyOrder(t, log,
		"systemctl show --property=RestartKillSignal --value podlazd.service",
		"systemctl show --property=KillMode --value podlazd.service",
		"systemctl show --property=RuntimeDirectoryPreserve --value podlazd.service",
		"sync -f "+h.runtimeDir,
		"systemctl daemon-reload",
		"systemctl show --property=RestartKillSignal --value podlazd.service",
		"systemctl show --property=KillMode --value podlazd.service",
		"systemctl show --property=RuntimeDirectoryPreserve --value podlazd.service",
		"deb-systemd-invoke try-restart podlazd.service",
		"systemctl show --property=Result --value podlazd.service",
		"systemctl daemon-reload",
		"systemctl show --property=RestartKillSignal --value podlazd.service",
		"systemctl show --property=KillMode --value podlazd.service",
		"systemctl show --property=RuntimeDirectoryPreserve --value podlazd.service",
	)
	if strings.Count(log, "deb-systemd-invoke try-restart podlazd.service") != 1 {
		t.Fatalf("expected exactly one restart mutation, log:\n%s", log)
	}
	if strings.Contains(log, "deb-systemd-invoke start podlazd.service") {
		t.Fatalf("legacy cutover must not retry with start, log:\n%s", log)
	}
	if !strings.Contains(log, "legacy override RestartKillSignal=SIGKILL KillMode=control-group") {
		t.Fatalf("legacy cutover must explicitly replace daemon and Xray cgroup without TimeoutStopSec fallback, log:\n%s", log)
	}
}

func TestPostinstallTreatsSystemdTimeoutResultAsFailure(t *testing.T) {
	h := newLegacyReplacementHarness(t, "timeout")
	log, err := h.run(t)
	if err == nil {
		t.Fatalf("postinstall accepted systemd Result=timeout; log:\n%s", log)
	}
	if strings.Contains(log, "deb-systemd-invoke start podlazd.service") {
		t.Fatalf("timeout failure must not be retried with start, log:\n%s", log)
	}
	if _, statErr := os.Stat(filepath.Join(h.runtimeDir, "legacy-upgrade-continuation")); statErr != nil {
		t.Fatalf("timeout failure must retain migration authority: %v", statErr)
	}
}

type legacyReplacementHarness struct {
	binDir, logPath, runtimeDir, bootIDPath, systemdRuntimeDir string
	evidence                                                   []string
}

func newLegacyReplacementHarness(t *testing.T, result string) legacyReplacementHarness {
	t.Helper()
	root := t.TempDir()
	h := legacyReplacementHarness{
		binDir: filepath.Join(root, "bin"), logPath: filepath.Join(root, "calls.log"),
		runtimeDir: filepath.Join(root, "runtime"), bootIDPath: filepath.Join(root, "boot-id"),
		systemdRuntimeDir: filepath.Join(root, "systemd-run"),
	}
	for _, dir := range []string{h.binDir, h.runtimeDir, h.systemdRuntimeDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(h.bootIDPath, []byte("boot-example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tx := filepath.Join(h.runtimeDir, "transactions", "tx-example.json")
	cfg := filepath.Join(h.runtimeDir, "generated", "core-example.json")
	for _, p := range []string{tx, cfg} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("evidence\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h.evidence = []string{tx, cfg}
	state := filepath.Join(root, "reloaded")
	resultPath := filepath.Join(root, "result")
	if err := os.WriteFile(resultPath, []byte(result), 0o600); err != nil {
		t.Fatal(err)
	}
	override := filepath.Join(h.systemdRuntimeDir, "podlazd.service.d", "50-podlaz-legacy-replacement.conf")

	writeLegacyStub(t, h.binDir, "systemctl", fmt.Sprintf(`#!/bin/sh
printf 'systemctl %%s\n' "$*" >> %q
case "$1" in
  is-enabled|is-active) exit 0 ;;
  daemon-reload) : > %q; exit 0 ;;
  show)
    case "$*" in
      *RestartKillSignal*)
        if [ ! -e %q ]; then printf '15\n'; elif [ -e %q ]; then printf '9\n'; else printf '10\n'; fi
        ;;
      *KillMode*)
        if [ -e %q ]; then printf 'control-group\n'; else printf 'mixed\n'; fi
        ;;
      *RuntimeDirectoryPreserve*)
        if [ ! -e %q ]; then printf 'no\n'; else printf 'yes\n'; fi
        ;;
      *Result*) cat %q ;;
    esac
    exit 0
    ;;
esac
exit 0
`, h.logPath, state, state, override, override, state, resultPath))
	writeLegacyStub(t, h.binDir, "sync", fmt.Sprintf(`#!/bin/sh
printf 'sync %%s\n' "$*" >> %q
exit 0
`, h.logPath))
	writeLegacyStub(t, h.binDir, "deb-systemd-invoke", fmt.Sprintf(`#!/bin/sh
printf 'deb-systemd-invoke %%s\n' "$*" >> %q
if [ "$1" = try-restart ]; then
  test -s %q || exit 31
  test -s %q || exit 32
  test -s %q || exit 33
  test -s %q || exit 34
  grep -q 'RestartKillSignal=SIGKILL' %q || exit 35
  grep -q 'KillMode=control-group' %q || exit 36
  printf 'legacy override RestartKillSignal=SIGKILL KillMode=control-group\n' >> %q
fi
exit 0
`, h.logPath, filepath.Join(h.runtimeDir, "legacy-upgrade-continuation"), tx, cfg, override, override, override, h.logPath))
	for _, name := range []string{"systemd-sysusers", "deb-systemd-helper"} {
		writeLegacyStub(t, h.binDir, name, fmt.Sprintf("#!/bin/sh\nprintf '%s %%s\\n' \"$*\" >> %q\nexit 0\n", name, h.logPath))
	}
	return h
}

func (h legacyReplacementHarness) run(t *testing.T) (string, error) {
	t.Helper()
	cmd := exec.Command("sh", "postinstall", "configure")
	cmd.Dir = "."
	cmd.Env = append(os.Environ(),
		"PATH="+h.binDir+":"+os.Getenv("PATH"),
		"PODLAZ_RUNTIME_DIR="+h.runtimeDir,
		"PODLAZ_BOOT_ID_PATH="+h.bootIDPath,
		"PODLAZ_SYSTEMD_RUNTIME_DIR="+h.systemdRuntimeDir,
		"PODLAZ_MAINTSCRIPT_RUN_DIR="+filepath.Join(h.runtimeDir, "maint"),
	)
	output, err := cmd.CombinedOutput()
	log, _ := os.ReadFile(h.logPath)
	return string(log) + string(output), err
}

func writeLegacyStub(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func assertLegacyOrder(t *testing.T, log string, expected ...string) {
	t.Helper()
	pos := 0
	for _, want := range expected {
		next := strings.Index(log[pos:], want)
		if next < 0 {
			t.Fatalf("missing %q after offset %d; log:\n%s", want, pos, log)
		}
		pos += next + len(want)
	}
}
