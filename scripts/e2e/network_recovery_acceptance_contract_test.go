package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestNetworkRecoveryCandidateUpgradeDoesNotManuallyStartService(t *testing.T) {
	data, err := os.ReadFile("network-recovery-package-acceptance.sh")
	if err != nil {
		t.Fatalf("read network-recovery acceptance: %v", err)
	}
	script := string(data)
	candidate := shellFunctionBody(t, script, "install_candidate_package")
	if !strings.Contains(candidate, "apt install") {
		t.Fatal("candidate install helper must install the candidate package")
	}
	if strings.Contains(candidate, "systemctl start") || strings.Contains(candidate, "systemctl restart") {
		t.Fatal("candidate package acceptance must not repair podlazd after apt install")
	}
	if strings.Count(candidate, "apt install") != 1 {
		t.Fatal("candidate package acceptance must admit exactly one package mutation")
	}
	for _, required := range []string{"systemctl is-active", "main_pid", "previous_pid"} {
		if !strings.Contains(candidate, required) {
			t.Fatalf("candidate package acceptance must verify %q after package replacement", required)
		}
	}
	if strings.Count(script, "run_installed_podlaz connect --mode tun") != 1 {
		t.Fatal("network recovery acceptance must issue exactly one CLI connect, on the lower release")
	}
}

func TestNetworkRecoveryBaselineSetupMayStartServiceExplicitly(t *testing.T) {
	data, err := os.ReadFile("network-recovery-package-acceptance.sh")
	if err != nil {
		t.Fatalf("read network-recovery acceptance: %v", err)
	}
	baseline := shellFunctionBody(t, string(data), "install_setup_package")
	if !strings.Contains(baseline, "systemctl start") {
		t.Fatal("baseline setup helper may explicitly start podlazd to establish the test fixture")
	}
}

func TestNetworkRecoveryPinsOfficialV029PackageProvenance(t *testing.T) {
	data, err := os.ReadFile("network-recovery-package-acceptance.sh")
	if err != nil {
		t.Fatalf("read network-recovery acceptance: %v", err)
	}
	script := string(data)
	for _, required := range []string{
		"c846f5465a90a50d72f3fc393d639a402d590798",
		"91644dee9ca92ddc5c48793b926f20d18da4d4267cbfdd3b41303e1e5c52516e",
		"74a4fe360fc0b05ec419440ae6f54ec3b76f9679a525671d1a905142920fa673",
		`sha256sum "${PODLAZ_E2E_BASE_DEB}"`,
		"baseline package digest",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("historical v0.2.29 provenance lost %q", required)
		}
	}
}

func TestNetworkRecoveryStatusUsesSemanticFiniteStateModel(t *testing.T) {
	data, err := os.ReadFile("network-recovery-package-acceptance.sh")
	if err != nil {
		t.Fatalf("read network-recovery acceptance: %v", err)
	}
	script := string(data)
	classifier := shellFunctionBody(t, script, "daemon_status_classify")
	for _, required := range []string{
		"connection", "mode", "tun_health", "transactions", "requires_cleanup",
		"startup_scan", "network_session", "resume_stage", "last_resume_outcome",
		"TARGET_REACHED", "PROGRESS_POSSIBLE", "TERMINAL_IMPOSSIBLE", "INCOMPATIBLE",
	} {
		if !strings.Contains(classifier, required) {
			t.Fatalf("semantic status classifier must inspect/emit %q", required)
		}
	}
	if strings.Contains(classifier, `status.get("tun")`) || strings.Contains(classifier, `.get('tun')`) {
		t.Fatal("human-readable tun presentation must not determine lifecycle state")
	}
	waiter := shellFunctionBody(t, script, "wait_for_semantic_status")
	for _, required := range []string{"TERMINAL_IMPOSSIBLE", "INCOMPATIBLE", "capture_failure_evidence"} {
		if !strings.Contains(waiter, required) {
			t.Fatalf("semantic waiter must fast-fail/capture on %q", required)
		}
	}
}

func TestNetworkRecoveryCapturesFailureEvidenceBeforeCleanup(t *testing.T) {
	data, err := os.ReadFile("network-recovery-package-acceptance.sh")
	if err != nil {
		t.Fatalf("read network-recovery acceptance: %v", err)
	}
	script := string(data)
	finish := shellFunctionBody(t, script, "finish")
	capture := strings.Index(finish, "capture_failure_evidence")
	cleanupCall := strings.Index(finish, "\n  cleanup ||")
	if capture < 0 || cleanupCall < 0 || capture > cleanupCall {
		t.Fatal("failure evidence must be captured before cleanup mutates host state")
	}
	captureBody := shellFunctionSection(t, script, "capture_failure_evidence", "capture_pre_upgrade_snapshot")
	for _, required := range []string{
		"systemd --version", "uname -a", "dpkg-query", "systemctl show", "MainPID",
		"RestartKillSignal", "KillMode", "TimeoutStopUSec", "FragmentPath",
		"network-session-continuation.json", "network-session-resume.json", "transactions",
	} {
		if !strings.Contains(captureBody, required) {
			t.Fatalf("failure evidence must capture %q", required)
		}
	}
}

func TestNetworkRecoveryLegacyUpgradeSuccessProvesReconstructionNotSameSessionID(t *testing.T) {
	data, err := os.ReadFile("network-recovery-package-acceptance.sh")
	if err != nil {
		t.Fatalf("read network-recovery acceptance: %v", err)
	}
	script := string(data)
	proof := shellFunctionBody(t, script, "assert_legacy_upgrade_converged")
	for _, required := range []string{
		"legacy-upgrade-continuation", "network-session-continuation.json", "intent",
		"resume", "current_boot", "committed", "requires_cleanup", "verified",
	} {
		if !strings.Contains(proof, required) {
			t.Fatalf("legacy success proof must include %q", required)
		}
	}
	if strings.Contains(proof, "session_before") || strings.Contains(proof, "same_session") {
		t.Fatal("v0.2.29 predates Network Session IDs; legacy success must prove reconstruction, not ID equality")
	}
}

func TestNetworkRecoveryRejectsTimeoutFallbackAfterCandidateInstall(t *testing.T) {
	data, err := os.ReadFile("network-recovery-package-acceptance.sh")
	if err != nil {
		t.Fatalf("read network-recovery acceptance: %v", err)
	}
	proof := shellFunctionBody(t, string(data), "assert_package_replacement_transition")
	for _, required := range []string{"Result", "timeout", "ExecMainCode", "ExecMainStatus", "RestartKillSignal", "KillMode", "TimeoutStopUSec"} {
		if !strings.Contains(proof, required) {
			t.Fatalf("package replacement proof must inspect %q", required)
		}
	}
}

func shellFunctionBody(t *testing.T, script, name string) string {
	t.Helper()
	startMarker := name + "() {"
	start := strings.Index(script, startMarker)
	if start < 0 {
		t.Fatalf("missing shell function %s", name)
	}
	bodyStart := start + len(startMarker)
	end := strings.Index(script[bodyStart:], "\n}")
	if end < 0 {
		t.Fatalf("unterminated shell function %s", name)
	}
	return script[bodyStart : bodyStart+end]
}

func shellFunctionSection(t *testing.T, script, startName, nextName string) string {
	t.Helper()
	startMarker := startName + "() {"
	start := strings.Index(script, startMarker)
	if start < 0 {
		t.Fatalf("missing shell function %s", startName)
	}
	nextMarker := "\n" + nextName + "() {"
	end := strings.Index(script[start:], nextMarker)
	if end < 0 {
		t.Fatalf("missing following shell function %s", nextName)
	}
	return script[start : start+end]
}
