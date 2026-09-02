package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBoundedInstalledClientPreservesTimeoutAndLocaleContract(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("lib", "bounded_client.sh"))
	if err != nil {
		t.Fatalf("read bounded client library: %v", err)
	}
	text := string(contents)
	for _, fragment := range []string{
		"run_installed_podlaz_bounded()",
		`timeout --signal=TERM --kill-after=5s "${timeout_seconds}"`,
		`runuser -u "$(id -un)" -g podlaz`,
		`LC_ALL=C`,
		`XDG_CONFIG_HOME="${XDG_CONFIG_HOME}"`,
		`XDG_STATE_HOME="${XDG_STATE_HOME}"`,
		`XDG_CACHE_HOME="${XDG_CACHE_HOME}"`,
		`/usr/bin/podlaz "$@"`,
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("bounded client library missing %q", fragment)
		}
	}
}
