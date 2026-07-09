package doctor

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestRunWithOptionsReportsResolvedpodlazDNSDiagnosticLine(t *testing.T) {
	report := RunWithOptions(context.Background(), Options{
		Runner: fakeRunner{
			paths: map[string]string{
				"ip":         "/usr/sbin/ip",
				"resolvectl": "/usr/bin/resolvectl",
			},
			commands: map[string]fakeCommand{
				"ip route show default": {
					stdout: "default via 192.0.2.1 dev wlp0s20f3",
				},
				"ip link show dev podlaz0": {
					stdout: "7: podlaz0: <POINTOPOINT,UP> mtu 1500",
				},
				"resolvectl status podlaz0 --no-pager": {
					stdout: "Link 7 (podlaz0)\n    DNS Domain: ~.",
				},
			},
		},
		RuntimeDir: filepath.Join(t.TempDir(), "podlaz"),
	})

	assertCheck(t, report, "resolved", SeverityWarning, "podlaz DNS route-only domain ~. is active on podlaz0")
}

func TestRunWithOptionsReportsMissingResolvedpodlazDNSAsOK(t *testing.T) {
	report := RunWithOptions(context.Background(), Options{
		Runner: fakeRunner{
			paths: map[string]string{
				"ip":         "/usr/sbin/ip",
				"resolvectl": "/usr/bin/resolvectl",
			},
			commands: map[string]fakeCommand{
				"ip route show default": {
					stdout: "default via 192.0.2.1 dev wlp0s20f3",
				},
				"ip link show dev podlaz0": {
					stderr:   "Device \"podlaz0\" does not exist.",
					exitCode: 1,
					err:      errors.New("exit status 1"),
				},
				"resolvectl status podlaz0 --no-pager": {
					stderr:   "Link podlaz0 does not exist.",
					exitCode: 1,
					err:      errors.New("exit status 1"),
				},
			},
		},
		RuntimeDir: filepath.Join(t.TempDir(), "podlaz"),
	})

	assertCheck(t, report, "resolved", SeverityOK, "no podlaz-owned DNS state found for podlaz0")
}
