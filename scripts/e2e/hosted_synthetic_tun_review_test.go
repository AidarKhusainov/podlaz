package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostedSyntheticTUNReportValidationIsFailClosed(t *testing.T) {
	keys := []string{
		"candidate.provenance",
		"ordinary_user.boundary",
		"tun.verified_active",
		"tun.system_dns",
		"tun.https_tls",
		"tun.doctor",
		"tun.clean_disconnect",
		"tun.terminal_cleanup",
		"tun.recovery_clean",
		"guest.baseline_restored",
		"outer.cleanup",
		"artifact.privacy",
	}

	run := func(overrides map[string]string) error {
		t.Helper()
		root := t.TempDir()
		var report strings.Builder
		for _, key := range keys {
			state := "pass"
			if override, ok := overrides[key]; ok {
				state = override
			}
			report.WriteString(key + "=" + state + "\n")
		}
		report.WriteString("failure.class=none\n")
		report.WriteString("failure.step=none\n")
		if err := os.WriteFile(filepath.Join(root, "hosted-synthetic-tun.txt"), []byte(report.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("bash", hostedSyntheticTUNScript, "validate-report")
		cmd.Env = append(os.Environ(), "E2E_ARTIFACT_DIR="+root, "E2E_TMP_ROOT="+filepath.Join(root, "private"))
		return cmd.Run()
	}

	if err := run(nil); err != nil {
		t.Fatalf("all-pass required report rejected: %v", err)
	}
	if err := run(map[string]string{"tun.doctor": "observed"}); err != nil {
		t.Fatalf("topology-dependent doctor observation rejected: %v", err)
	}
	for name, overrides := range map[string]map[string]string{
		"required observed":    {"candidate.provenance": "observed"},
		"required unavailable": {"tun.system_dns": "unavailable"},
		"doctor unavailable":   {"tun.doctor": "unavailable"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(overrides); err == nil {
				t.Fatal("report validator accepted a non-success required evidence state")
			}
		})
	}
}

func TestHostedSyntheticTUNVerifiedActiveUsesExactPersistedAuthority(t *testing.T) {
	script := readHostedSyntheticTUNFile(t, hostedSyntheticTUNScript)
	start := strings.Index(script, "assert_verified_active_authority() {")
	end := strings.Index(script, "\nrun_active_traffic_checks() {")
	if start < 0 || end <= start {
		t.Fatal("verified-active authority function boundaries not found")
	}
	active := script[start:end]
	requireHostedSyntheticTUNMarkers(t, active,
		"${ACTIVE_AUTHORITY_HELPER}",
		"${GUEST_PRIVATE}/status.json",
		"/run/podlaz/transactions",
		"/run/podlaz/network-session-continuation.json",
		"/proc/sys/kernel/random/boot_id",
		"/run/podlaz/generated/xray.json",
		"resolved-dns.txt",
		"resolved-domain.txt",
		"resolved-default-route.txt",
		"nft-ruleset.json",
	)
	forbidHostedSyntheticTUNMarkers(t, active,
		"resolvectl status podlaz0",
		"nft list table inet podlaz",
		"nft list tables | grep -E 'table inet podlaz_pe_",
		"test -s /run/podlaz/network-session-continuation.json",
		"test -s /run/podlaz/generated/xray.json",
	)
}

func TestHostedSyntheticTUNActiveAuthorityNegativeMatrix(t *testing.T) {
	cmd := exec.Command("python3", "hosted_synthetic_active_authority_contract.py")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("active authority contract: %v\n%s", err, output)
	}
}

func TestHostedSyntheticTUNDoctorUsesStructuredCanonicalSemantics(t *testing.T) {
	script := readHostedSyntheticTUNFile(t, hostedSyntheticTUNScript)
	start := strings.Index(script, "run_tun_doctor() {")
	end := strings.Index(script, "\nassert_terminal_authority_clean() {")
	if start < 0 || end <= start {
		t.Fatal("doctor function boundaries not found")
	}
	doctor := script[start:end]
	requireHostedSyntheticTUNMarkers(t, doctor,
		"doctor --tun --json",
		"healthy",
		"degraded",
		"ipv6_not_present",
	)
	forbidHostedSyntheticTUNMarkers(t, doctor, "3) record_evidence tun.doctor observed")
}

func TestHostedSyntheticTUNFailureClassificationDoesNotPrejudgeProduct(t *testing.T) {
	script := readHostedSyntheticTUNFile(t, hostedSyntheticTUNScript)
	requireHostedSyntheticTUNMarkers(t, script,
		"diagnostic_unknown",
		"mark_failure diagnostic_unknown profile.import",
		"mark_failure diagnostic_unknown tun.connect",
		"mark_failure diagnostic_unknown tun.active_traffic",
		"mark_failure diagnostic_unknown tun.doctor",
		"mark_failure diagnostic_unknown guest.connectivity_restored",
	)
}

func TestHostedSyntheticTUNArchitectureDistinguishesProductMutationFromHostedPlumbing(t *testing.T) {
	architecture := readHostedSyntheticTUNFile(t, "../../ARCHITECTURE.md")
	requireHostedSyntheticTUNMarkers(t, architecture,
		"Podlaz-owned destructive networking never mutates the outer hosted runner",
		"infrastructure-owned guest plumbing",
		"Direct destructive product networking on a host remains dedicated-only",
	)
}
