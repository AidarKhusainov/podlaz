package doctor

import (
	"context"
	"path/filepath"
	"testing"
)

func TestRunWithOptionsReportsResolvedpodlazDNSDiagnosticLine(t *testing.T) {
	report := RunWithOptions(context.Background(), Options{
		Runner: fakeRunner{
			paths: map[string]string{
				"resolvectl": "/usr/bin/resolvectl",
			},
			commands: map[string]fakeCommand{
				"resolvectl status podlaz0 --no-pager": {
					stdout: "Link 7 (podlaz0)\n    DNS Domain: ~.",
				},
			},
		},
		RuntimeDir: filepath.Join(t.TempDir(), "podlaz"),
	})

	assertCheck(t, report, "resolved", SeverityOK, "podlaz DNS route-only domain ~. active on podlaz0")
}
