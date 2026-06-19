package status

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestInspectWithOptionsReportsCleanInactiveWhenRuntimeMissing(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "missing")

	report := InspectWithOptions(context.Background(), Options{RuntimeDir: runtimeDir})

	if report.HasUnhealthyState() {
		t.Fatalf("missing runtime directory should be a clean inactive state: %#v", report)
	}
	if report.Service != "none" {
		t.Fatalf("expected no service in local fallback, got %q", report.Service)
	}
	if report.Connection != "inactive" {
		t.Fatalf("expected inactive connection, got %q", report.Connection)
	}
	if report.RuntimeDirectory.State != RuntimeDirectoryMissing {
		t.Fatalf("expected missing runtime directory, got %#v", report.RuntimeDirectory)
	}

	got := report.String()
	want := []string{
		"podlaz status\n",
		"Daemon: not running\n",
		"Service: none\n",
		"Connection: inactive\n",
		"Daemon socket: missing\n",
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
	runtimeDir := filepath.Join(t.TempDir(), "podlaz")
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
		"Daemon socket: missing\n",
		"Runtime directory: present (stale)\n",
		"Stale state: found 1 recovery candidate\n",
		"Recovery candidates:\n",
		"  - runtime directory: " + runtimeDir + "\n",
		"Guidance: run `podlaz recover` for the canonical read-only recovery dry-run.\n",
	}
	for _, text := range want {
		if !strings.Contains(got, text) {
			t.Fatalf("expected output to contain %q, got %q", text, got)
		}
	}
}

func TestInspectWithOptionsDefersStaleClassificationWhenDaemonSocketInaccessible(t *testing.T) {
	runtimeDir := shortSocketRuntimeDir(t)
	generatedDir := filepath.Join(runtimeDir, generatedDirName)
	if err := os.MkdirAll(generatedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(runtimeDir, "podlazd.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	report := InspectWithOptions(context.Background(), Options{
		RuntimeDir:         runtimeDir,
		SocketPath:         socketPath,
		DaemonSocketAccess: DaemonSocketAccessPermissionDenied,
	})

	if report.DaemonSocket.State != DaemonSocketInaccessible {
		t.Fatalf("expected inaccessible daemon socket, got %#v", report.DaemonSocket)
	}
	if !report.HasUnhealthyState() {
		t.Fatal("inaccessible daemon socket should keep status unhealthy")
	}
	if report.Connection != "unknown (inspection incomplete)" {
		t.Fatalf("expected incomplete connection state, got %q", report.Connection)
	}
	assertNoCandidate(t, report, "runtime-directory")
	assertNoCandidate(t, report, "generated-runtime-configs")

	got := report.String()
	want := []string{
		"Daemon socket: present but inaccessible (permission denied; check podlaz group membership)\n",
		"Runtime directory: present (daemon socket inaccessible; stale status unknown)\n",
		"Stale state: unknown (inspection incomplete)\n",
		"permission denied; local runtime state may belong to a live podlazd and was not classified as stale\n",
		"Guidance: run `podlaz doctor` for diagnostic detail.\n",
	}
	for _, text := range want {
		if !strings.Contains(got, text) {
			t.Fatalf("expected output to contain %q, got %q", text, got)
		}
	}
	if strings.Contains(got, "Recovery candidates:") {
		t.Fatalf("inaccessible daemon-owned runtime should not be rendered as stale candidates, got %q", got)
	}
}

func TestInspectWithOptionsDefersStaleClassificationWhenSocketPathInspectionDenied(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "podlaz")
	socketPath := filepath.Join(runtimeDir, "podlazd.sock")
	generatedDir := filepath.Join(runtimeDir, generatedDirName)
	transactionsDir := filepath.Join(runtimeDir, "transactions")
	if err := os.MkdirAll(generatedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(transactionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(transactionsDir, "tx.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	originalLstat := lstat
	lstat = func(path string) (os.FileInfo, error) {
		if path == socketPath {
			return nil, &os.PathError{Op: "lstat", Path: path, Err: os.ErrPermission}
		}
		return originalLstat(path)
	}
	defer func() { lstat = originalLstat }()

	report := InspectWithOptions(context.Background(), Options{
		RuntimeDir:         runtimeDir,
		SocketPath:         socketPath,
		DaemonSocketAccess: DaemonSocketAccessPermissionDenied,
	})

	if report.DaemonSocket.State != DaemonSocketInaccessible {
		t.Fatalf("expected inaccessible daemon socket, got %#v", report.DaemonSocket)
	}
	if report.Connection != "unknown (inspection incomplete)" {
		t.Fatalf("expected incomplete connection state, got %q", report.Connection)
	}
	assertNoCandidate(t, report, "runtime-directory")
	assertNoCandidate(t, report, "generated-runtime-configs")
	assertNoCandidate(t, report, "transaction-state")

	got := report.String()
	for _, want := range []string{
		"Daemon socket: inaccessible (permission denied; check podlaz group membership)\n",
		"permission denied while inspecting daemon socket path\n",
		"Stale state: unknown (inspection incomplete)\n",
		"local runtime state may belong to a live podlazd and was not classified as stale\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected output to contain %q, got %q", want, got)
		}
	}
	if strings.Contains(got, "Recovery candidates:") || strings.Contains(got, "generated runtime configs") || strings.Contains(got, "transaction rollback state") {
		t.Fatalf("permission-denied socket path inspection must not expose stale candidates, got %q", got)
	}
}

func TestInspectWithOptionsReportsUnexpectedDaemonSocketPath(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "podlaz")
	if err := os.Mkdir(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(runtimeDir, "podlazd.sock")
	if err := os.WriteFile(socketPath, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}

	report := InspectWithOptions(context.Background(), Options{RuntimeDir: runtimeDir, SocketPath: socketPath})

	if report.DaemonSocket.State != DaemonSocketUnexpected {
		t.Fatalf("expected unexpected daemon socket path, got %#v", report.DaemonSocket)
	}
	assertCandidate(t, report, "daemon-socket", "daemon socket path", socketPath)
	assertCandidate(t, report, "runtime-directory", "runtime directory", runtimeDir)

	got := report.String()
	want := []string{
		"Daemon socket: present as non-socket path (stale)\n",
		"Stale state: found 2 recovery candidates\n",
		"  - daemon socket path: " + socketPath + "\n",
	}
	for _, text := range want {
		if !strings.Contains(got, text) {
			t.Fatalf("expected output to contain %q, got %q", text, got)
		}
	}
}

func TestInspectWithOptionsReportsGeneratedRuntimeConfigs(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "podlaz")
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

func TestInspectWithOptionsReportsTransactionState(t *testing.T) {
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

	report := InspectWithOptions(context.Background(), Options{RuntimeDir: runtimeDir})
	if !report.HasUnhealthyState() {
		t.Fatal("pending transaction state should be unhealthy")
	}
	assertCandidate(t, report, "transaction-state", "transaction rollback state", path)

	got := report.String()
	want := []string{
		"Transaction: pending apply\n",
		"Rollback available: yes\n",
		"State path: " + path + "\n",
	}
	for _, text := range want {
		if !strings.Contains(got, text) {
			t.Fatalf("expected output to contain %q, got %q", text, got)
		}
	}
}

func TestInspectWithOptionsReportsRuntimePath(t *testing.T) {
	runtimePath := filepath.Join(t.TempDir(), "podlaz")
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
		Service:          "none",
		Connection:       "unknown (inspection incomplete)",
		RuntimeDirectory: RuntimeDirectory{Message: "unknown (inspection incomplete)"},
		Proxy:            "inactive",
		TUN:              "not managed in this build",
		Candidates: []Candidate{{
			Description: "generated runtime config path",
			Target:      "https://example.com/sub?credential=sample-query-value",
		}},
		Warnings: []Warning{{
			Target:  "profile 123e4567-e89b-12d3-a456-426614174000",
			Message: "password=example-password-value token=example-token-value",
		}},
	}

	got := report.String()
	for _, forbidden := range []string{"sample-query-value", "example-password-value", "example-token-value", "123e4567-e89b-12d3-a456-426614174000"} {
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

func assertNoCandidate(t *testing.T, report Report, kind string) {
	t.Helper()
	for _, candidate := range report.Candidates {
		if candidate.Kind == kind {
			t.Fatalf("candidate kind %q should not be present in %#v", kind, report.Candidates)
		}
	}
}

func shortSocketRuntimeDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "tw-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
