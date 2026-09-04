package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func executableShellLines(script string) string {
	var lines []string
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func TestRemoteClientAcceptancePreservesOrdinaryUserReadOnlyAccess(t *testing.T) {
	data, err := os.ReadFile("remote-client-acceptance.sh")
	if err != nil {
		t.Fatalf("read remote-client acceptance: %v", err)
	}
	script := string(data)
	for _, required := range []string{
		"id -nG",
		"ordinary-user acceptance must not run as root",
		"ordinary_user_without_podlaz_group",
		"status",
		"doctor",
		"recover --json",
		`logs "--${mode}" --since 36h`,
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("remote-client acceptance lost %q", required)
		}
	}

	for _, forbidden := range []string{
		"PODLAZ_E2E_PROFILE_URI",
		"PODLAZ_E2E_PROFILE_URI_LIST",
		"PODLAZ_XRAY_PATH",
		"PODLAZ_E2E_PKCHECK_MODE_FILE",
		"connect --mode",
		"runuser",
		"usermod",
		"gpasswd",
		"e2e-tun-package-convergence.yml",
	} {
		if strings.Contains(executableShellLines(script), forbidden) {
			t.Fatalf("ordinary-user read-only acceptance must not mutate lifecycle or identity: %q", forbidden)
		}
	}
}
