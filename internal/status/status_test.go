package status

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectWithOptionsReportsCleanInactiveWhenRuntimeMissing(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "missing")

	report := InspectWithOptions(context.Background(), Options{RuntimeDir: runtimeDir})

	if report.HasUnhealthyState() {
		t.Fatalf("missing runtime directory should be a clean inactive state: %#v", report)
	}
	if report.Connection != "inactive" {
		t.Fatalf("expected inactive connection, got %q", report.Connection)
	}
	if report.RuntimeDirectory.State != RuntimeDirectoryMissing {
		t.Fatalf("expected missing runtime directory, got %#v", report.RuntimeDirectory)
	}

	got := report.String()
	want := []string{
		"TunWarden status\n",
		"Daemon: not running\n",
		"Connection: inactive\n",
		"Runtime directory: missing\n",
		"Proxy: inactive\n",
		"TUN: not managed in this build\n",
		"Stale state: none\n",
	}
	for _, text := range want {
		if !strings.Contains(got, text) {
			t.Fatalf("expected output to contain %q, got %q", text, got)
		}
	}
	if strings.Contains(got, "Guidance:") {
		t.Fatalf("clean status should not print recovery guidance, got %q", got)
	}
}

func TestInspectWithOptionsReportsStaleRuntimeDirectory(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "tunwarden")
	if err := os.Mkdir(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	report := InspectWithOptions(context.Background(), Options{RuntimeDir: runtimeDir})

	if !report.HasUnhealthyState() {
		t.Fatal("stale runtime directory should be unhealthy")
	}
	if report.Connection != "inactive (stale state detected)" {
		t.Fatalf("expected stale inactive connection, got %q", report.Connection)
	}
	if report.RuntimeDirectory.State != RuntimeDirectoryPresent {
		t.Fatalf("expected present runtime directory, got %#v", report.RuntimeDirectory)
	}
	assertCandidate(t, report, "runtime-directory", "runtime directory", runtimeDir)

	got := report.String()
	want := []string{
		"Runtime directory: present (stale)\n",
		"Stale state: found 1 recovery candidate\n",
		"Recovery candidates:\n",
		"  - runtime directory: " + runtimeDir + "\n",
		"Guidance: run `tunwarden recover` for the canonical read-only recovery dry-run.\n",
	}
	for _, text := range want {
		if !strings.Contains(got, text) {
			t.Fatalf("expected output to contain %q, got %q", text, got)
		}
	}
}

func TestInspectWithOptionsReportsGeneratedRuntimeConfigs(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "tunwarden")
	generatedDir := filepath.Join(runtimeDir, generatedDirName)
	if err := os.MkdirAll(generatedDir, 0o755); err != nil {
		t.Fatal(err)
	}

	report := InspectWithOptions(context.Background(), Options{RuntimeDir: runtimeDir})

	assertCandidate(t, report, "generated-runtime-configs", "generated runtime configs", generatedDir)
	assertCandidate(t, report, "runtime-directory", "runtime directory", runtimeDir)

	got := report.String()
	if !strings.Contains(got, "Stale state: found 2 recovery candidates\n") {
		t.Fatalf("expected two recovery candidates, got %q", got)
	}
	if !strings.Contains(got, "  - generated runtime configs: "+generatedDir+"\n") {
		t.Fatalf("expected generated runtime config candidate, got %q", got)
	}
}

func TestInspectWithOptionsReportsRuntimePath(t *testing.T) {
	runtimePath := filepath.Join(t.TempDir(), "tunwarden")
	if err := os.WriteFile(runtimePath, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	report := InspectWithOptions(context.Background(), Options{RuntimeDir: runtimePath})

	if report.RuntimeDirectory.State != RuntimeDirectoryPath {
		t.Fatalf("expected runtime path state, got %#v", report.RuntimeDirectory)
	}
	assertCandidate(t, report, "runtime-directory", "runtime path", runtimePath)
	if got := report.String(); !strings.Contains(got, "Runtime directory: present as non-directory (stale)\n") {
		t.Fatalf("expected runtime path output, got %q", got)
	}
}

func TestInspectWithOptionsRedactsSensitiveOutput(t *testing.T) {
	report := Report{
		Daemon:           "not running",
		Connection:       "unknown (inspection incomplete)",
		RuntimeDirectory: RuntimeDirectory{Message: "unknown (inspection incomplete)"},
		Proxy:            "inactive",
		TUN:              "not managed in this build",
		Candidates: []Candidate{{
			Description: "generated runtime config path",
			Target:      "https://example.com/sub?token=very-secret-token",
		}},
		Warnings: []Warning{{
			Target:  "profile 123e4567-e89b-12d3-a456-426614174000",
			Message: "password=hunter2 token=abcdef",
		}},
	}

	got := report.String()
	for _, forbidden := range []string{"very-secret-token", "hunter2", "abcdef", "123e4567-e89b-12d3-a456-426614174000"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("status output leaked %q in %q", forbidden, got)
		}
	}
	for _, want := range []string{"https://example.com/sub?REDACTED", "password=REDACTED", "token=REDACTED", "123e…4000"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected redacted output to contain %q, got %q", want, got)
		}
	}
}

func assertCandidate(t *testing.T, report Report, kind string, description string, target string) {
	t.Helper()
	for _, candidate := range report.Candidates {
		if candidate.Kind == kind && candidate.Description == description && candidate.Target == target {
			return
		}
	}
	t.Fatalf("candidate %q/%q/%q not found in %#v", kind, description, target, report.Candidates)
}
