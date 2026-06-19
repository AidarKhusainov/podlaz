package doctor

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
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
	report := successfulReport(t)

	if report.Source != SourceLocalFallback {
		t.Fatalf("expected local fallback source, got %q", report.Source)
	}
	assertCheckOrder(t, report, []string{
		"platform",
		"iproute2",
		"default-route",
		"default-interface",
		"networkmanager",
		"systemd",
		"resolved",
		"nftables",
		"stale-resources",
	})
	assertCheck(t, report, "iproute2", SeverityOK, "ip found at /usr/sbin/ip")
	assertCheck(t, report, "default-route", SeverityOK, "default via 192.0.2.1 dev wlp0s20f3")
	assertCheck(t, report, "default-interface", SeverityOK, "wlp0s20f3")
	assertCheck(t, report, "networkmanager", SeverityOK, "nmcli found at /usr/bin/nmcli")
	assertCheck(t, report, "systemd", SeverityOK, "systemctl found at /usr/bin/systemctl")
	assertCheck(t, report, "resolved", SeverityOK, "resolvectl found at /usr/bin/resolvectl")
	assertCheck(t, report, "nftables", SeverityOK, "nft found at /usr/sbin/nft")
	assertCheck(t, report, "stale-resources", SeverityOK, "no podlaz-owned resources found")

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
		RuntimeDir: filepath.Join(t.TempDir(), "podlaz"),
	})

	assertCheck(t, report, "iproute2", SeverityWarning, "ip not found")
	assertCheck(t, report, "default-route", SeverityWarning, "ip is unavailable")
	assertCheck(t, report, "default-interface", SeverityWarning, "ip is unavailable")
	assertCheck(t, report, "networkmanager", SeverityWarning, "nmcli not found")
	assertCheck(t, report, "systemd", SeverityWarning, "systemctl not found")
	assertCheck(t, report, "resolved", SeverityWarning, "resolvectl not found")
	assertCheck(t, report, "nftables", SeverityWarning, "nft not found")
	assertCheck(t, report, "stale-resources", SeverityWarning, "cannot inspect interface podlaz0 because ip is unavailable")

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
				"ip link show dev podlaz0": {
					stderr:   "Device \"podlaz0\" does not exist.",
					exitCode: 1,
					err:      errors.New("exit status 1"),
				},
				"nft list table inet podlaz": {
					stderr:   "Error: No such file or directory",
					exitCode: 1,
					err:      errors.New("exit status 1"),
				},
			},
		},
		RuntimeDir: filepath.Join(t.TempDir(), "podlaz"),
	})

	assertCheck(t, report, "default-route", SeverityFail, "ip route show default failed")
	assertCheck(t, report, "default-route", SeverityFail, "RTNETLINK answers: test failure")
	assertCheck(t, report, "default-interface", SeverityFail, "default route command failed")

	if !report.HasFailures() {
		t.Fatal("failed diagnostic command must be reported as a failure")
	}
}

func TestRunWithOptionsPreservesStaleResourceWarnings(t *testing.T) {
	report := RunWithOptions(context.Background(), Options{
		Runner: fakeRunner{
			paths: map[string]string{
				"ip":  "/usr/sbin/ip",
				"nft": "/usr/sbin/nft",
			},
			commands: map[string]fakeCommand{
				"ip route show default": {
					stdout: "default via 192.0.2.1 dev wlp0s20f3",
				},
				"ip link show dev podlaz0": {
					stdout: "2: podlaz0: <POINTOPOINT,UP> mtu 1500",
				},
				"nft list table inet podlaz": {
					stderr:   "Operation not permitted",
					exitCode: 1,
					err:      errors.New("exit status 1"),
				},
			},
		},
		RuntimeDir: filepath.Join(t.TempDir(), "podlaz"),
	})

	assertCheck(t, report, "stale-resources", SeverityWarning, "found interface podlaz0 exists")
	assertCheck(t, report, "stale-resources", SeverityWarning, "incomplete checks: cannot inspect nft table inet podlaz")
	assertCheck(t, report, "stale-resources", SeverityWarning, "Operation not permitted")
}

func TestRunWithOptionsReportsTransactionStateInStaleResources(t *testing.T) {
	runtimeDir := t.TempDir()
	store := txstate.TransactionStore{RuntimeDir: runtimeDir}
	tx := txstate.NewTransaction("tx-apply", "profile-1", "tun", time.Now().UTC())
	tx.State = txstate.TransactionApplying
	tx.Rollback = txstate.RollbackMetadata{
		TUN: []txstate.TUNRollback{{
			InterfaceName: "podlaz0",
			Owner:         txstate.TransactionOwner,
		}},
	}
	path, err := store.Save(tx)
	if err != nil {
		t.Fatalf("save transaction: %v", err)
	}

	report := RunWithOptions(context.Background(), Options{
		Runner:     missingResourcesRunner(),
		RuntimeDir: runtimeDir,
	})

	assertCheck(t, report, "stale-resources", SeverityWarning, "transaction tx-apply pending apply")
	assertCheck(t, report, "stale-resources", SeverityWarning, "rollback available: yes")
	assertCheck(t, report, "stale-resources", SeverityWarning, "state path: "+path)
}

func TestRunWithOptionsDoesNotTreatLiveDaemonRuntimeDirAsStale(t *testing.T) {
	runtimeDir := t.TempDir()
	report := RunWithOptions(context.Background(), Options{
		Runner:                  missingResourcesRunner(),
		RuntimeDir:              runtimeDir,
		RuntimeDirOwnedByDaemon: true,
	})

	assertCheck(t, report, "stale-resources", SeverityOK, "no podlaz-owned resources found")
}

func TestReportStringIncludesSource(t *testing.T) {
	report := Report{Source: SourceDaemon, Checks: []Check{{Name: "daemon", Severity: SeverityOK, Message: "running"}}}
	got := report.String()
	if !strings.Contains(got, "Source: daemon") {
		t.Fatalf("expected source line, got %q", got)
	}
}

func TestReportStringRedactsDiagnostics(t *testing.T) {
	uuid := "123e4567-e89b-12d3-a456-426614174000"
	report := Report{
		Source: "source token=source-secret " + uuid,
		Checks: []Check{{
			Name:     "check password=name-secret " + uuid,
			Severity: SeverityWarning,
			Message:  "failed with token=message-secret password=message-password trace_id=" + uuid,
		}},
	}

	got := report.String()
	for _, leaked := range []string{"source-secret", "name-secret", "message-secret", "message-password", uuid} {
		if strings.Contains(got, leaked) {
			t.Fatalf("doctor output leaked %q in %q", leaked, got)
		}
	}
	for _, want := range []string{"token=REDACTED", "password=REDACTED", "123e…4000"} {
		if !strings.Contains(got, want) {
			t.Fatalf("doctor output should contain redacted marker %q, got %q", want, got)
		}
	}
}

func TestDaemonConversionPreservesReportModel(t *testing.T) {
	report := Report{Source: SourceDaemon, Checks: []Check{{Name: "daemon", Severity: SeverityOK, Message: "running"}}}
	response := ToDaemon(report)
	if err := api.ValidateDoctorResponse(response); err != nil {
		t.Fatalf("daemon response should be valid: %v", err)
	}
	converted := FromDaemon(response)
	if converted.Source != SourceDaemon {
		t.Fatalf("expected daemon source, got %q", converted.Source)
	}
	assertCheck(t, converted, "daemon", SeverityOK, "running")
}

func successfulReport(t *testing.T) Report {
	t.Helper()

	return RunWithOptions(context.Background(), Options{
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
				"ip link show dev podlaz0": {
					stderr:   "Device \"podlaz0\" does not exist.",
					exitCode: 1,
					err:      errors.New("exit status 1"),
				},
				"nft list table inet podlaz": {
					stderr:   "Error: No such file or directory",
					exitCode: 1,
					err:      errors.New("exit status 1"),
				},
			},
		},
		RuntimeDir: filepath.Join(t.TempDir(), "podlaz"),
	})
}

func missingResourcesRunner() fakeRunner {
	return fakeRunner{
		paths: map[string]string{
			"ip":         "/usr/sbin/ip",
			"nft":        "/usr/sbin/nft",
			"nmcli":      "/usr/bin/nmcli",
			"systemctl":  "/usr/bin/systemctl",
			"resolvectl": "/usr/bin/resolvectl",
		},
		commands: map[string]fakeCommand{
			"ip route show default": {
				stdout: "default via 192.0.2.1 dev wlp0s20f3 proto dhcp src 192.0.2.10 metric 600",
			},
			"ip link show dev podlaz0": {
				stderr:   "Device \"podlaz0\" does not exist.",
				exitCode: 1,
				err:      errors.New("exit status 1"),
			},
			"nft list table inet podlaz": {
				stderr:   "Error: No such file or directory",
				exitCode: 1,
				err:      errors.New("exit status 1"),
			},
		},
	}
}

func assertCheckOrder(t *testing.T, report Report, want []string) {
	t.Helper()

	if len(report.Checks) != len(want) {
		t.Fatalf("expected %d checks, got %d: %#v", len(want), len(report.Checks), report.Checks)
	}
	for i, name := range want {
		if got := report.Checks[i].Name; got != name {
			t.Fatalf("check %d: expected %q, got %q", i, name, got)
		}
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
