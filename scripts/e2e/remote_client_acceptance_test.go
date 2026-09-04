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

func TestRemoteClientAcceptanceUsesDeterministicOrdinaryUserFixture(t *testing.T) {
	data, err := os.ReadFile("remote-client-acceptance.sh")
	if err != nil {
		t.Fatalf("read remote-client acceptance: %v", err)
	}
	script := string(data)
	for _, required := range []string{
		"id -nG",
		"ordinary-user acceptance must not run as root",
		"ordinary_user_without_podlaz_group",
		"PODLAZ_XRAY_PATH",
		"remote-client.example.net",
		"connect --mode proxy-only",
		"recover --json",
		`logs "--${mode}" --since 36h`,
		"proxy_status_doctor_recover_consistent",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("remote-client acceptance lost %q", required)
		}
	}

	for _, forbidden := range []string{
		"PODLAZ_E2E_PROFILE_URI",
		"PODLAZ_E2E_PROFILE_URI_LIST",
		"e2e-tun-package-convergence.yml",
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("remote-client acceptance must be provider/workflow independent: %q", forbidden)
		}
	}

	executable := executableShellLines(script)
	for _, forbidden := range []string{
		"runuser",
		"usermod",
		"gpasswd",
		`-g podlaz`,
		`-G podlaz`,
		`-G "${LOG_READER_ACCESS_GROUP}"`,
	} {
		if strings.Contains(executable, forbidden) {
			t.Fatalf("ordinary-user acceptance must preserve the login identity without granting or rewriting groups in executable behavior: %q", forbidden)
		}
	}
}
