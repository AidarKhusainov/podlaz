package doctor

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner struct {
	paths    map[string]string
	commands map[string]fakeCommand
}

type fakeCommand struct {
	stdout   string
	stderr   string
	exitCode int
	err      error
}

func (r fakeRunner) LookPath(file string) (string, error) {
	path, ok := r.paths[file]
	if !ok {
		return "", errors.New("command not found")
	}
	return path, nil
}

func (r fakeRunner) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	key := filepath.Base(name) + " " + strings.Join(args, " ")
	command, ok := r.commands[key]
	if !ok {
		return CommandResult{ExitCode: -1}, errors.New("unexpected command: " + key)
	}
	return CommandResult{
		Stdout:   command.stdout,
		Stderr:   command.stderr,
		ExitCode: command.exitCode,
	}, command.err
}

func TestRunWithOptionsReportsSuccessfulLinuxDiagnostics(t *testing.T) {
	report := RunWithOptions(context.Background(), Options{
		Runner: fakeRunner{
			paths: map[string]string{
				"ip":         "/usr/sbin/ip",
				"nmcli":      "/usr/bin/nmcli",
				"systemctl":  "/usr/bin/systemctl",
				"resolvectl": "/usr/bin/resolvectl",
				"nft":        "/usr/sbin/nft",
			},
			commands: map[string]fakeCommand{
				"ip route show default": {
					stdout: "default via 192.0.2.1 dev wlp0s20f3 proto dhcp src 192.0.2.10 metric 600",
				},
				"ip link show dev tunwarden0": {
					stderr:   "Device \"tunwarden0\" does not exist.",
					exitCode: 1,
					err:      errors.New("exit status 1"),
				},
				"nft list table inet tunwarden": {
					stderr:   "Error: No such file or directory",
					exitCode: 1,
					err:      errors.New("exit status 1"),
				},
			},
		},
		RuntimeDir: filepath.Join(t.TempDir(), "tunwarden"),
	})

	assertCheck(t, report, "iproute2", SeverityOK, "ip found at /usr/sbin/ip")
	assertCheck(t, report, "default-route", SeverityOK, "default via 192.0.2.1 dev wlp0s20f3")
	assertCheck(t, report, "default-interface", SeverityOK, "wlp0s20f3")
	assertCheck(t, report, "networkmanager", SeverityOK, "nmcli found at /usr/bin/nmcli")
	assertCheck(t, report, "systemd", SeverityOK, "systemctl found at /usr/bin/systemctl")
	assertCheck(t, report, "resolved", SeverityOK, "resolvectl found at /usr/bin/resolvectl")
	assertCheck(t, report, "nftables", SeverityOK, "nft found at /usr/sbin/nft")
	assertCheck(t, report, "stale-resources", SeverityOK, "no TunWarden-owned resources found")

	if report.HasFailures() {
		t.Fatal("successful diagnostics must not report failures")
	}
}

func TestRunWithOptionsWarnsWhenCommandsAreMissing(t *testing.T) {
	report := RunWithOptions(context.Background(), Options{
		Runner: fakeRunner{
			paths:    map[string]string{},
			commands: map[string]fakeCommand{},
		},
		RuntimeDir: filepath.Join(t.TempDir(), "tunwarden"),
	})

	assertCheck(t, report, "iproute2", SeverityWarning, "ip not found")
	assertCheck(t, report, "default-route", SeverityWarning, "ip is unavailable")
	assertCheck(t, report, "default-interface", SeverityWarning, "ip is unavailable")
	assertCheck(t, report, "networkmanager", SeverityWarning, "nmcli not found")
	assertCheck(t, report, "systemd", SeverityWarning, "systemctl not found")
	assertCheck(t, report, "resolved", SeverityWarning, "resolvectl not found")
	assertCheck(t, report, "nftables", SeverityWarning, "nft not found")
	assertCheck(t, report, "stale-resources", SeverityWarning, "cannot inspect interface tunwarden0 because ip is unavailable")

	if report.HasFailures() {
		t.Fatal("missing optional commands should warn, not fail")
	}
}

func TestRunWithOptionsReportsCommandFailures(t *testing.T) {
	report := RunWithOptions(context.Background(), Options{
		Runner: fakeRunner{
			paths: map[string]string{
				"ip":         "/usr/sbin/ip",
				"systemctl":  "/usr/bin/systemctl",
				"resolvectl": "/usr/bin/resolvectl",
				"nft":        "/usr/sbin/nft",
			},
			commands: map[string]fakeCommand{
				"ip route show default": {
					stderr:   "RTNETLINK answers: test failure",
					exitCode: 2,
					err:      errors.New("exit status 2"),
				},
				"ip link show dev tunwarden0": {
					stderr:   "Device \"tunwarden0\" does not exist.",
					exitCode: 1,
					err:      errors.New("exit status 1"),
				},
				"nft list table inet tunwarden": {
					stderr:   "Error: No such file or directory",
					exitCode: 1,
					err:      errors.New("exit status 1"),
				},
			},
		},
		RuntimeDir: filepath.Join(t.TempDir(), "tunwarden"),
	})

	assertCheck(t, report, "default-route", SeverityFail, "ip route show default failed")
	assertCheck(t, report, "default-route", SeverityFail, "RTNETLINK answers: test failure")
	assertCheck(t, report, "default-interface", SeverityFail, "default route command failed")

	if !report.HasFailures() {
		t.Fatal("failed diagnostic command must be reported as a failure")
	}
}

func assertCheck(t *testing.T, report Report, name string, severity Severity, messageContains string) {
	t.Helper()

	for _, check := range report.Checks {
		if check.Name != name {
			continue
		}
		if check.Severity != severity {
			t.Fatalf("check %s: expected severity %s, got %s", name, severity, check.Severity)
		}
		if !strings.Contains(check.Message, messageContains) {
			t.Fatalf("check %s: expected message containing %q, got %q", name, messageContains, check.Message)
		}
		return
	}

	t.Fatalf("check %s not found in report: %#v", name, report.Checks)
}
