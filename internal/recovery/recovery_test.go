package recovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeScanner struct {
	result ScanResult
}

func (s fakeScanner) Scan(context.Context) ScanResult {
	return s.result
}

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

func TestPlanWithFakeScannerRendersRecoveryCandidates(t *testing.T) {
	plan := PlanWithOptions(context.Background(), Options{Scanner: fakeScanner{result: ScanResult{
		Candidates: []Candidate{
			{Kind: "tun-interface", Description: "TUN interface", Target: "tunwarden0"},
			{Kind: "nftables-table", Description: "nftables table", Target: "inet tunwarden"},
			{Kind: "generated-runtime-configs", Description: "generated runtime configs", Target: "/run/tunwarden/generated"},
			{Kind: "runtime-directory", Description: "runtime directory", Target: "/run/tunwarden"},
		},
	}}})

	got := plan.String()
	want := []string{
		"TunWarden recovery dry-run",
		"Would recover TUN interface: tunwarden0",
		"Would recover nftables table: inet tunwarden",
		"Would recover generated runtime configs: /run/tunwarden/generated",
		"Would recover runtime directory: /run/tunwarden",
		"No changes were applied.",
	}
	for _, text := range want {
		if !strings.Contains(got, text) {
			t.Fatalf("expected output to contain %q, got %q", text, got)
		}
	}
	if strings.Contains(got, "command:") {
		t.Fatalf("dry-run output must not render executable cleanup commands, got %q", got)
	}
}

func TestPlanWithFakeScannerRendersCleanHost(t *testing.T) {
	plan := PlanWithOptions(context.Background(), Options{Scanner: fakeScanner{}})

	got := plan.String()
	want := []string{
		"TunWarden recovery dry-run",
		"No TunWarden-owned recovery candidates found.",
		"No changes were applied.",
	}
	for _, text := range want {
		if !strings.Contains(got, text) {
			t.Fatalf("expected output to contain %q, got %q", text, got)
		}
	}
	if strings.Contains(got, "Would recover") {
		t.Fatalf("clean host output must not contain recovery candidates, got %q", got)
	}
}

func TestOSScannerDetectsOwnedResources(t *testing.T) {
	runtimeDir := t.TempDir()
	generatedDir := filepath.Join(runtimeDir, "generated")
	if err := os.MkdirAll(generatedDir, 0o755); err != nil {
		t.Fatalf("create generated dir: %v", err)
	}

	plan := PlanWithOptions(context.Background(), Options{
		RuntimeDir: runtimeDir,
		Runner: fakeRunner{
			paths: map[string]string{
				"ip":  "/usr/sbin/ip",
				"nft": "/usr/sbin/nft",
			},
			commands: map[string]fakeCommand{
				"ip link show dev tunwarden0": {
					stdout: "2: tunwarden0: <POINTOPOINT,UP> mtu 1500",
				},
				"nft list table inet tunwarden": {
					stdout: "table inet tunwarden {}",
				},
			},
		},
	})

	assertCandidate(t, plan, "tun-interface", "tunwarden0")
	assertCandidate(t, plan, "nftables-table", "inet tunwarden")
	assertCandidate(t, plan, "generated-runtime-configs", generatedDir)
	assertCandidate(t, plan, "runtime-directory", runtimeDir)
	if len(plan.Warnings) != 0 {
		t.Fatalf("expected no warnings, got %#v", plan.Warnings)
	}
}

func TestOSScannerRendersCleanHostFromMissingOwnedResources(t *testing.T) {
	plan := PlanWithOptions(context.Background(), Options{
		RuntimeDir: filepath.Join(t.TempDir(), "tunwarden"),
		Runner: fakeRunner{
			paths: map[string]string{
				"ip":  "/usr/sbin/ip",
				"nft": "/usr/sbin/nft",
			},
			commands: map[string]fakeCommand{
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
	})

	if len(plan.Candidates) != 0 {
		t.Fatalf("expected clean host, got candidates %#v", plan.Candidates)
	}
	got := plan.String()
	if !strings.Contains(got, "No TunWarden-owned recovery candidates found.") {
		t.Fatalf("expected clean host message, got %q", got)
	}
}

func TestOSScannerPreservesInspectionWarnings(t *testing.T) {
	plan := PlanWithOptions(context.Background(), Options{
		RuntimeDir: filepath.Join(t.TempDir(), "tunwarden"),
		Runner: fakeRunner{
			paths: map[string]string{
				"ip":  "/usr/sbin/ip",
				"nft": "/usr/sbin/nft",
			},
			commands: map[string]fakeCommand{
				"ip link show dev tunwarden0": {
					stderr:   "Operation not permitted",
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
	})

	if len(plan.Warnings) != 1 {
		t.Fatalf("expected one warning, got %#v", plan.Warnings)
	}
	got := plan.String()
	if !strings.Contains(got, "Warning: could not inspect TUN interface tunwarden0") || !strings.Contains(got, "Operation not permitted") {
		t.Fatalf("expected inspection warning in output, got %q", got)
	}
}

func assertCandidate(t *testing.T, plan PlanResult, kind string, target string) {
	t.Helper()
	for _, candidate := range plan.Candidates {
		if candidate.Kind == kind && candidate.Target == target {
			return
		}
	}
	t.Fatalf("candidate kind=%q target=%q not found in %#v", kind, target, plan.Candidates)
}
