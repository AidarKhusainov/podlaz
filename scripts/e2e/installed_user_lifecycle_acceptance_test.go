package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func readInstalledUserLifecycle(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("installed-user-lifecycle-acceptance.sh")
	if err != nil {
		t.Fatalf("read installed-user lifecycle acceptance: %v", err)
	}
	return string(data)
}

func TestInstalledUserLifecycleCoversPackagedAuthorizationBoundary(t *testing.T) {
	script := readInstalledUserLifecycle(t)
	for _, required := range []string{
		"id -nG",
		"must not run as root",
		"must not belong to the podlaz group",
		`stat -c '%U:%G:%a' /run/podlaz/podlazd.sock`,
		"root:podlaz:660",
		"filesystem socket unexpectedly reachable",
		"PODLAZ_E2E_PKCHECK_MODE_FILE",
		"authorization unavailable",
		"connect --mode proxy-only",
		"Status: Connected",
		"Mode: proxy-only",
		"Status: Disconnected",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("installed-user authorization contract lost %q", required)
		}
	}
	for _, forbidden := range []string{"usermod", "gpasswd", "newgrp", "PODLAZ_XRAY_PATH="} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("installed-user authorization fixture must not use %q", forbidden)
		}
	}
}

func TestInstalledUserLifecycleCoversSupervisedXrayCrash(t *testing.T) {
	script := readInstalledUserLifecycle(t)
	for _, required := range []string{
		"/usr/lib/podlaz/xray",
		"pgrep -P",
		"kill -KILL",
		"core exited unexpectedly; inspect podlaz logs --core",
		"127.0.0.1:1080",
		"127.0.0.1:8080",
		"recover --json",
		"unexpected recovery candidates",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("installed-user Xray crash contract lost %q", required)
		}
	}
}

func TestInstalledPackageIntegrationRunsInstalledUserLifecycle(t *testing.T) {
	data, err := os.ReadFile("installed-package-integration.sh")
	if err != nil {
		t.Fatalf("read installed-package integration: %v", err)
	}
	if !strings.Contains(string(data), `bash "${SCRIPT_DIR}/installed-user-lifecycle-acceptance.sh"`) {
		t.Fatal("hosted installed-package integration must run the focused installed-user lifecycle acceptance")
	}
}
