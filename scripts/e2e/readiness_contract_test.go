package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadinessLibraryExposesDistinctSemanticPrimitives(t *testing.T) {
	library := filepath.Join("lib", "readiness.sh")
	contents, err := os.ReadFile(library)
	if err != nil {
		t.Fatalf("read readiness library: %v", err)
	}
	text := string(contents)
	for _, function := range []string{
		"wait_for_daemon_socket()",
		"wait_for_daemon_ready()",
		"wait_for_service_active()",
	} {
		if !strings.Contains(text, function) {
			t.Fatalf("readiness library does not declare %s", function)
		}
	}
}

func TestWaitForDaemonSocketRequiresOnlySocketReadiness(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "daemon.sock")
	listenerScript := `import socket,sys,time
s=socket.socket(socket.AF_UNIX)
s.bind(sys.argv[1])
time.sleep(2)`
	listener := exec.Command("python3", "-c", listenerScript, socket)
	if err := listener.Start(); err != nil {
		t.Fatalf("start unix socket fixture: %v", err)
	}
	defer func() { _ = listener.Process.Kill(); _, _ = listener.Process.Wait() }()

	cmd := exec.Command("bash", "-c", `
set -eu
fail() { printf '%s\n' "$*" >&2; return 1; }
source ./lib/readiness.sh
wait_for_daemon_socket "$1" 1
`, "bash", socket)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("wait_for_daemon_socket failed: %v\n%s", err, output)
	}
}

func TestWaitForDaemonReadyRequiresSocketAndActiveService(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	systemctl := filepath.Join(binDir, "systemctl")
	if err := os.WriteFile(systemctl, []byte("#!/usr/bin/env bash\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sudo := filepath.Join(binDir, "sudo")
	if err := os.WriteFile(sudo, []byte("#!/usr/bin/env bash\n[[ $1 == -n ]] && shift\nexec \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "daemon.sock")
	listenerScript := `import socket,sys,time
s=socket.socket(socket.AF_UNIX)
s.bind(sys.argv[1])
time.sleep(2)`
	listener := exec.Command("python3", "-c", listenerScript, socket)
	if err := listener.Start(); err != nil {
		t.Fatalf("start unix socket fixture: %v", err)
	}
	defer func() { _ = listener.Process.Kill(); _, _ = listener.Process.Wait() }()

	cmd := exec.Command("bash", "-c", `
set -eu
fail() { return 7; }
source ./lib/readiness.sh
wait_for_daemon_ready "$1" podlazd.service 1
`, "bash", socket)
	cmd.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"))
	if err := cmd.Run(); err == nil {
		t.Fatal("wait_for_daemon_ready succeeded while service stayed inactive")
	} else if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 7 {
		t.Fatalf("wait_for_daemon_ready failure = %v, want fail() exit 7", err)
	}
}

func TestWaitForServiceActivePollsSystemdWithoutSocketDependency(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	counter := filepath.Join(dir, "counter")
	systemctl := filepath.Join(binDir, "systemctl")
	script := `#!/usr/bin/env bash
count=0
[[ -f "${COUNTER_FILE}" ]] && count=$(cat "${COUNTER_FILE}")
count=$((count + 1))
printf '%s' "${count}" >"${COUNTER_FILE}"
[[ ${count} -ge 2 ]]
`
	if err := os.WriteFile(systemctl, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	sudo := filepath.Join(binDir, "sudo")
	if err := os.WriteFile(sudo, []byte("#!/usr/bin/env bash\n[[ $1 == -n ]] && shift\nexec \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "-c", `
set -eu
fail() { printf '%s\n' "$*" >&2; return 1; }
source ./lib/readiness.sh
wait_for_service_active podlazd.service 1
`)
	cmd.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"), "COUNTER_FILE="+counter)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("wait_for_service_active failed: %v\n%s", err, output)
	}
}
