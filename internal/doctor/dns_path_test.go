package doctor

import (
	"context"
	"path/filepath"
	"testing"
)

func TestRunWithOptionsReportsResolvedTunWardenDNSDiagnosticLine(t *testing.T) {
	report := RunWithOptions(context.Background(), Options{
		Runner: fakeRunner{
			paths: map[string]string{
				"resolvectl": "/usr/bin/resolvectl",
			},
			commands: map[string]fakeCommand{
				"resolvectl status tunwarden0 --no-pager": {
					stdout: "Link 7 (tunwarden0)\n    DNS Domain: ~.",
				},
			},
		},
		RuntimeDir: filepath.Join(t.TempDir(), "tunwarden"),
	})

	assertCheck(t, report, "resolved", SeverityOK, "TunWarden DNS route-only domain ~. active on tunwarden0")
}
