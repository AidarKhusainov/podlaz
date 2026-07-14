package doctor

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestRunWithOptionsTreatsResolvedNoSuchDeviceAsMissing(t *testing.T) {
	report := RunWithOptions(context.Background(), Options{
		Runner: fakeRunner{
			paths: map[string]string{
				"ip":         "/usr/sbin/ip",
				"resolvectl": "/usr/bin/resolvectl",
			},
			commands: map[string]fakeCommand{
				"ip route show default": {
					stdout: "default via 192.0.2.1 dev eth0",
				},
				"resolvectl status podlaz0 --no-pager": {
					stderr:   `Failed to resolve interface "podlaz0": No such device`,
					exitCode: 1,
					err:      errors.New("exit status 1"),
				},
			},
		},
		RuntimeDir: filepath.Join(t.TempDir(), "podlaz"),
	})

	assertCheck(t, report, "resolved", SeverityOK, "no podlaz-owned DNS state found for podlaz0")
}
