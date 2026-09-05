package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestDataPlaneUsesHostedCompatibleInstalledClientBoundary(t *testing.T) {
	data, err := os.ReadFile("data-plane.sh")
	if err != nil {
		t.Fatalf("read data-plane script: %v", err)
	}
	script := string(data)

	for _, required := range []string{
		`source "${SCRIPT_DIR}/lib/installed_client.sh"`,
		"runuser",
		"run_installed_podlaz",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("data-plane hosted client execution lost %q", required)
		}
	}

	if strings.Contains(script, `sudo -n -u "$(id -un)" -g podlaz`) {
		t.Fatal("data-plane must not require a sudo runas-group grant on hosted runners")
	}
}
