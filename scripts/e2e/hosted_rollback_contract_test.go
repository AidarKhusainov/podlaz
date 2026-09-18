package e2e_test

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

const (
	hostedRollbackWorkflow = "../../.github/workflows/hosted-rollback.yml"
	hostedApplyRollback    = "hosted-apply-rollback.sh"
	hostedVerifyRollback   = "hosted-verify-rollback.sh"
)

func TestHostedSyntheticTunCanBeSourcedWithoutRunningMain(t *testing.T) {
	data, err := os.ReadFile("hosted-synthetic-tun.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, marker := range []string{
		`if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then`,
		"main \"$@\"",
	} {
		if !strings.Contains(script, marker) {
			t.Fatalf("hosted synthetic TUN source contract lost %q", marker)
		}
	}
}

func TestHostedRollbackWorkflowKeepsApplyAndVerifySeparate(t *testing.T) {
	data, err := os.ReadFile(hostedRollbackWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	for _, marker := range []string{
		"apply-rollback:",
		"name: Network apply rollback",
		"bash scripts/e2e/hosted-apply-rollback.sh",
		"hosted-apply-rollback.txt",
		"verify-rollback:",
		"name: Network verify rollback",
		"bash scripts/e2e/hosted-verify-rollback.sh",
		"hosted-verify-rollback.txt",
		"runs-on: ubuntu-24.04",
		"persist-credentials: false",
	} {
		if !strings.Contains(workflow, marker) {
			t.Fatalf("hosted rollback workflow lost %q", marker)
		}
	}
	for _, action := range []string{"actions/checkout", "actions/setup-go", "actions/upload-artifact"} {
		re := regexp.MustCompile(regexp.QuoteMeta("uses: "+action+"@") + `[0-9a-f]{40}`)
		if !re.MatchString(workflow) {
			t.Fatalf("%s must remain pinned to an immutable commit", action)
		}
	}
}

func TestHostedApplyRollbackUsesExistingPostMutationHook(t *testing.T) {
	assertHostedRollbackScenario(t, hostedApplyRollback, []string{
		`source "${SCRIPT_DIR}/hosted-synthetic-tun.sh"`,
		"PODLAZ_E2E_TUN_HOOK_PHASE=tun-address-apply",
		"tun-address-apply-injected",
		"failure_phase=network-apply",
		"primary_classification=tun_address_apply_failure",
		"diagnostics-persisted",
		"rollback-started",
		"rollback-completed",
		"assert_owned_state_absent",
		"assert_foreign_sentinel",
		"assert_recovery_clean",
		"connect.failed",
		"diagnostic.truth",
		"rollback.owned_absent",
		"foreign.nft_preserved",
		"recovery.clean",
		"artifact.privacy",
	})
}

func TestHostedVerifyRollbackUsesExistingVerificationHook(t *testing.T) {
	assertHostedRollbackScenario(t, hostedVerifyRollback, []string{
		`source "${SCRIPT_DIR}/hosted-synthetic-tun.sh"`,
		"PODLAZ_E2E_TUN_HOOK_PHASE=network-verify",
		"network-verify-injected",
		"failure_phase=network-verify",
		"primary_classification=network_verify_failure",
		"diagnostics-persisted",
		"rollback-started",
		"rollback-completed",
		"assert_owned_state_absent",
		"assert_foreign_sentinel",
		"assert_recovery_clean",
		"connect.failed",
		"diagnostic.truth",
		"rollback.owned_absent",
		"foreign.nft_preserved",
		"recovery.clean",
		"artifact.privacy",
	})
}

func assertHostedRollbackScenario(t *testing.T, path string, markers []string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	script := string(data)
	for _, marker := range markers {
		if !strings.Contains(script, marker) {
			t.Fatalf("%s lost %q", path, marker)
		}
	}
	for _, forbidden := range []string{
		"PODLAZ_E2E_PROFILE_URI",
		"qemu-system",
		"systemctl suspend",
		"rtcwake",
		"reboot",
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("%s contains forbidden %q", path, forbidden)
		}
	}
	cmd := exec.Command("bash", "-n", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n %s: %v\n%s", path, err, out)
	}
}
