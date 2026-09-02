package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvidenceWritersKeepSchemasNarrow(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("lib", "evidence.sh"))
	if err != nil {
		t.Fatalf("read evidence library: %v", err)
	}
	text := string(contents)
	for _, function := range []string{"append_evidence_kv()", "append_evidence_pass()"} {
		if !strings.Contains(text, function) {
			t.Fatalf("evidence library missing %s", function)
		}
	}

	output := filepath.Join(t.TempDir(), "evidence.txt")
	cmd := exec.Command("bash", "-c", `
set -eu
fail() { return 9; }
source ./lib/evidence.sh
append_evidence_kv "$1" package_arch amd64
append_evidence_pass "$1" daemon_ready
`, "bash", output)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("write evidence: %v\n%s", err, combined)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "package_arch=amd64\ndaemon_ready=pass\n"; got != want {
		t.Fatalf("evidence = %q, want %q", got, want)
	}
}

func TestEvidenceWriterRejectsUnsafePassKey(t *testing.T) {
	cmd := exec.Command("bash", "-c", `
set -eu
fail() { return 9; }
source ./lib/evidence.sh
append_evidence_pass "$1" 'bad key'
`, "bash", filepath.Join(t.TempDir(), "evidence.txt"))
	if err := cmd.Run(); err == nil {
		t.Fatal("append_evidence_pass accepted an unsafe key")
	} else if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 9 {
		t.Fatalf("unsafe evidence key failure = %v, want fail() exit 9", err)
	}
}

func TestProfileInputReturnsFirstConfiguredURIWithoutFallbackMutation(t *testing.T) {
	cmd := exec.Command("bash", "-c", `
set -eu
source ./lib/profile_input.sh
first_configured_profile_uri
`)
	cmd.Env = append(os.Environ(),
		"PODLAZ_E2E_PROFILE_URI=",
		"PODLAZ_E2E_PROFILE_URI_LIST=vless://first.example\nvless://second.example",
	)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("resolve first profile: %v", err)
	}
	if got, want := string(output), "vless://first.example\n"; got != want {
		t.Fatalf("profile = %q, want %q", got, want)
	}
}

func TestHostObservationIsReadOnlyAndPrivacyScoped(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("lib", "host_observation.sh"))
	if err != nil {
		t.Fatalf("read host observation library: %v", err)
	}
	text := string(contents)
	for _, required := range []string{
		"observe_host_sensitive_values()",
		"hostname -f",
		"ip -o -4 addr show scope global",
		"ip -o -6 addr show scope global",
		"ip -4 route show default",
		"sort -u",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("host observation library missing %q", required)
		}
	}
	for _, forbidden := range []string{" ip link del ", " nft delete ", " resolvectl revert ", " systemctl restart "} {
		if strings.Contains(" "+text+" ", forbidden) {
			t.Fatalf("host observation library contains mutation %q", strings.TrimSpace(forbidden))
		}
	}
}
