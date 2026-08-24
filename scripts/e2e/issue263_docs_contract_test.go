package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestIssue263CanonicalDocsDescribeAutostartAndProductLifecycle(t *testing.T) {
	checks := map[string][]string{
		"../../docs/cli.md": {
			"podlaz autostart enable [--mode proxy-only|tun] <profile-id>",
			"Autostart: Enabled for next boot",
			"Status: Connected",
			"Status: Reconnecting",
		},
		"../../docs/state-and-security.md": {
			"Boot Autostart Manifest",
			"one logical autostart attempt per boot",
			"Network Session continuation",
		},
		"../../docs/debian-package.md": {
			"StateDirectory=podlaz",
			"boot-autostart-manifest.json",
			"daemon restart",
		},
		"../../docs/e2e.md": {
			"issue263-package-acceptance.sh",
			"terminal_no_same_boot_retry",
			"package upgrade",
		},
		"../../docs/man/podlaz.1": {
			"autostart enable",
			"Autostart: Enabled for next boot",
			"Status: Connected",
		},
		"../../docs/man/podlazd.8": {
			"Boot Autostart Manifest",
			"one logical autostart attempt per boot",
			"StateDirectory",
		},
	}

	for path, required := range checks {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		content := string(data)
		for _, needle := range required {
			if !strings.Contains(content, needle) {
				t.Errorf("%s must contain %q", path, needle)
			}
		}
	}
}
