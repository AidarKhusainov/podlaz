package e2e_test

import (
	"os"
	"os/exec"
	"strings"
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

func TestE2ETunFaultInjectionExercisesPostMutationAddressFailure(t *testing.T) {
	content, err := os.ReadFile("tun-fault-injection.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)
	for _, want := range []string{
		"run_apply_failure_probe tun-address-apply",
		"tun_address_apply_failure",
		"tun-address-apply-injected",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("fault-injection script does not prove %q", want)
		}
	}
}
