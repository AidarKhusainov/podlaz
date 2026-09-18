package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestHostedDaemonRecoveryControlHelpersAreNounsetSafe(t *testing.T) {
	data, err := os.ReadFile("hosted-daemon-recovery.sh")
	if err != nil {
		t.Fatalf("read hosted-daemon-recovery.sh: %v", err)
	}
	script := string(data)
	for _, unsafe := range []string{
		`local phase="$1" ready="${CONTROL_DIR}/${phase}.ready"`,
		`local phase="$1" ready="${CONTROL_DIR}/${phase}.ready" continue="${CONTROL_DIR}/${phase}.continue"`,
	} {
		if strings.Contains(script, unsafe) {
			t.Fatalf("control helper is unsafe under set -u: %s", unsafe)
		}
	}
}
