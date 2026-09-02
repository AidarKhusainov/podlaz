package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstalledClientWrapperUsesPodlazServiceGroupBoundary(t *testing.T) {
	path := filepath.Join("lib", "installed_client.sh")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read installed client library: %v", err)
	}
	text := string(contents)
	for _, fragment := range []string{
		"run_installed_podlaz()",
		`runuser -u "$(id -un)" -g podlaz`,
		`XDG_CONFIG_HOME="${XDG_CONFIG_HOME}"`,
		`XDG_STATE_HOME="${XDG_STATE_HOME}"`,
		`XDG_CACHE_HOME="${XDG_CACHE_HOME}"`,
		`/usr/bin/podlaz "$@"`,
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("installed client library missing %q", fragment)
		}
	}
	if strings.Contains(text, "timeout --signal") || strings.Contains(text, "LC_ALL=C") {
		t.Fatal("unbounded shared installed client wrapper absorbed bounded/local execution semantics")
	}
}

func TestStatusPollingUsesScenarioPredicateAndExplicitTimeout(t *testing.T) {
	path := filepath.Join("lib", "status_polling.sh")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read status polling library: %v", err)
	}
	if !strings.Contains(string(contents), "wait_for_status_match()") {
		t.Fatal("status polling library does not expose wait_for_status_match")
	}

	dir := t.TempDir()
	counter := filepath.Join(dir, "counter")
	cmd := exec.Command("bash", "-c", `
set -eu
fail() { printf '%s\n' "$*" >&2; return 1; }
source ./lib/status_polling.sh
predicate() {
  local count=0
  [[ -f "$1" ]] && count=$(cat "$1")
  count=$((count + 1))
  printf '%s' "$count" >"$1"
  [[ "$count" -ge 2 ]]
}
wait_for_status_match "fixture" 1 predicate "$1"
`, "bash", counter)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("status poll failed: %v\n%s", err, output)
	}
}

func TestPackageProvenanceKeepsAssertionsComposable(t *testing.T) {
	path := filepath.Join("lib", "package_provenance.sh")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read package provenance library: %v", err)
	}
	text := string(contents)
	for _, function := range []string{
		"assert_installed_podlaz_commit()",
		"assert_package_service_active()",
		"assert_native_deb_arch()",
		"assert_installed_package_version_matches_deb()",
	} {
		if !strings.Contains(text, function) {
			t.Fatalf("package provenance library missing %s", function)
		}
	}
}
