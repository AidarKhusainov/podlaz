package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestDataPlaneUsesPublicStatusContract(t *testing.T) {
	data, err := os.ReadFile("data-plane.sh")
	if err != nil {
		t.Fatalf("read data-plane script: %v", err)
	}
	script := string(data)

	for _, required := range []string{
		`Status: Connected`,
		`Status: Disconnected`,
		`Mode: proxy-only`,
		`DAEMON_RUNTIME_CONFIG_PATH="${DAEMON_SOCKET%/*}/generated/xray.json"`,
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("data-plane public status contract lost %q", required)
		}
	}

	for _, forbidden := range []string{
		`Connection: active`,
		`Connection: inactive`,
		`Stale state: none`,
		`/^Runtime config:/`,
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("data-plane must not depend on internal status presentation %q", forbidden)
		}
	}
}
