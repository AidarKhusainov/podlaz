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

func TestE2ETunAddressApplyProvesRollbackBeforeRestartOrRecovery(t *testing.T) {
	content, err := os.ReadFile("tun-fault-injection.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)
	start := strings.Index(script, "run_apply_failure_probe()")
	if start < 0 {
		t.Fatal("run_apply_failure_probe body not found")
	}
	end := strings.Index(script[start:], "run_network_verify_probe()")
	if end < 0 {
		t.Fatal("run_network_verify_probe boundary not found")
	}
	body := script[start : start+end]
	for _, want := range []string{
		`assert_tun_owned_runtime_absent "rollback-${hook_phase}"`,
		`assert_no_stale_state "rollback-${hook_phase}"`,
		"connect-${hook_phase}-immediate-retry",
		"disconnect-${hook_phase}-immediate-retry",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("address rollback proof is missing %q", want)
		}
	}
	branchStart := strings.Index(body, `if [[ "${hook_phase}" == "tun-address-apply" ]]`)
	if branchStart < 0 {
		t.Fatal("tun-address-apply rollback branch not found")
	}
	branch := body[branchStart:]
	returnIndex := strings.Index(branch, "return 0")
	if returnIndex < 0 {
		t.Fatal("tun-address-apply rollback branch return not found")
	}
	branch = branch[:returnIndex]
	if strings.Contains(branch, "systemctl restart podlazd.service") || strings.Contains(branch, "recover --execute --yes") {
		t.Fatalf("address rollback proof still depends on restart/recovery:\n%s", branch)
	}
}
