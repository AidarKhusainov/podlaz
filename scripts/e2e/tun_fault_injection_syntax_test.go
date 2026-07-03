package e2e_test

import (
	"os/exec"
	"testing"
)

func TestE2ETunFaultInjectionScriptHasValidBashSyntax(t *testing.T) {
	cmd := exec.Command("bash", "-n", "tun-fault-injection.sh")
	cmd.Dir = "."
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash -n tun-fault-injection.sh failed: %v\n%s", err, output)
	}
}
