package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func readDataPlaneScript(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("data-plane.sh")
	if err != nil {
		t.Fatalf("read data-plane script: %v", err)
	}
	return string(data)
}

func TestDataPlaneUsesHostedCompatibleInstalledClientBoundary(t *testing.T) {
	script := readDataPlaneScript(t)

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

func TestDataPlaneKeepsSensitiveProfileOutputOutOfWorkflowLogs(t *testing.T) {
	script := readDataPlaneScript(t)

	for _, forbidden := range []string{
		`sed -e 's/^/stdout: /' "${LAST_STDOUT}"`,
		`sed -e 's/^/stderr: /' "${LAST_STDERR}"`,
		`expect_success validate-primary-proxy`,
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("data-plane can publish sensitive profile output via %q", forbidden)
		}
	}
	if !strings.Contains(script, `expect_sensitive_success validate-primary-proxy`) {
		t.Fatal("profile validation must use the sensitive command capture boundary")
	}
}
