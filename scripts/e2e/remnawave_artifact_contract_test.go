package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runRemnawaveArtifactScanner(t *testing.T, kind, dir string) (string, string, error) {
	t.Helper()
	cmd := exec.Command("bash", "scan-remnawave-artifacts.sh", kind)
	cmd.Env = append(os.Environ(), "E2E_ARTIFACT_DIR="+dir)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func writeRemnawaveReport(t *testing.T, dir, kind string, failed bool) string {
	t.Helper()
	var name string
	var evidence []string
	switch kind {
	case "proxy":
		name = "remnawave-proxy.txt"
		evidence = []string{
			"remnawave.proxy_data_plane",
			"remnawave.proxy_path_attribution",
			"remnawave.proxy_cleanup",
			"fixture.cleanup",
		}
	case "subscription":
		name = "remnawave-subscription.txt"
		evidence = []string{
			"remnawave.subscription_import",
			"remnawave.subscription_refresh",
			"remnawave.hwid_registration",
			"remnawave.hwid_stable",
			"remnawave.hwid_device_limit",
			"remnawave.rejected_state_preserved",
			"fixture.cleanup",
		}
	default:
		t.Fatalf("unknown report kind %q", kind)
	}

	verdict := "pass"
	failureClass := "none"
	failureStep := "none"
	if failed {
		verdict = "fail"
		failureClass = "fixture"
		failureStep = "fixture.cleanup"
	}
	lines := []string{
		"candidate.commit=" + strings.Repeat("a", 40),
		"candidate.package_sha256=" + strings.Repeat("b", 64),
		"remnawave.panel_version=3.4.5",
		"remnawave.node_version=3.4.2",
	}
	for _, key := range evidence {
		lines = append(lines, key+"="+verdict)
	}
	lines = append(lines, "failure.class="+failureClass, "failure.step="+failureStep)

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRemnawavePublicArtifactScannerIsFailClosed(t *testing.T) {
	for _, kind := range []string{"proxy", "subscription"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			report := writeRemnawaveReport(t, dir, kind, false)
			if stdout, stderr, err := runRemnawaveArtifactScanner(t, kind, dir); err != nil {
				t.Fatalf("normalized %s report rejected: %v\nstdout=%s\nstderr=%s", kind, err, stdout, stderr)
			}
			data, err := os.ReadFile(report)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(report, append(data, []byte("unexpected.raw=value\n")...), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := runRemnawaveArtifactScanner(t, kind, dir); err == nil {
				t.Fatalf("%s scanner accepted non-normalized evidence", kind)
			}
		})
	}
}

func TestRemnawavePublicArtifactScannerAcceptsNormalizedFailureEvidence(t *testing.T) {
	for _, kind := range []string{"proxy", "subscription"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			writeRemnawaveReport(t, dir, kind, true)
			if stdout, stderr, err := runRemnawaveArtifactScanner(t, kind, dir); err != nil {
				t.Fatalf("normalized %s failure report rejected: %v\nstdout=%s\nstderr=%s", kind, err, stdout, stderr)
			}
		})
	}
}

func TestRemnawavePublicArtifactScannerRejectsUnexpectedAndNestedFiles(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(map[bool]string{false: "unexpected", true: "nested"}[nested], func(t *testing.T) {
			dir := t.TempDir()
			writeRemnawaveReport(t, dir, "proxy", false)
			path := filepath.Join(dir, "unexpected.txt")
			if nested {
				path = filepath.Join(dir, "nested", "raw.txt")
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(path, []byte("private evidence\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := runRemnawaveArtifactScanner(t, "proxy", dir); err == nil {
				t.Fatal("Remnawave scanner accepted unexpected public evidence")
			}
		})
	}
}

func runRemnawaveFailureFinalizer(t *testing.T, artifactDir, privateDir, defaultClass, defaultStep string, twice bool) (string, string, error) {
	t.Helper()
	script := `
set -Eeuo pipefail
SCRIPT_DIR="$PWD"
source lib/remnawave_evidence.sh
REMNAWAVE_EVIDENCE[remnawave.proxy_data_plane]=fail
REMNAWAVE_EVIDENCE[remnawave.proxy_path_attribution]=fail
REMNAWAVE_EVIDENCE[remnawave.proxy_cleanup]=fail
REMNAWAVE_EVIDENCE[fixture.cleanup]=pass
remnawave_mark_private_command_failure "$PRIVATE_ROOT" "$DEFAULT_CLASS" "$DEFAULT_STEP"
remnawave_finalize_report "$REPORT" "$COMMIT" "$DIGEST" 3.4.5 3.4.2 \
  remnawave.proxy_data_plane remnawave.proxy_path_attribution remnawave.proxy_cleanup fixture.cleanup
if [[ "$TWICE" == true ]]; then
  remnawave_mark_failure diagnostic_unknown should.not.replace.finalized.report
  remnawave_finalize_report "$REPORT" "$COMMIT" "$DIGEST" 3.4.5 3.4.2 \
    remnawave.proxy_data_plane remnawave.proxy_path_attribution remnawave.proxy_cleanup fixture.cleanup
fi
`
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(os.Environ(),
		"PRIVATE_ROOT="+privateDir,
		"REPORT="+filepath.Join(artifactDir, "remnawave-proxy.txt"),
		"DEFAULT_CLASS="+defaultClass,
		"DEFAULT_STEP="+defaultStep,
		"COMMIT="+strings.Repeat("a", 40),
		"DIGEST="+strings.Repeat("b", 64),
		"TWICE="+map[bool]string{false: "false", true: "true"}[twice],
	)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func TestRemnawaveFailureEvidenceKeepsPrivateDiagnosticSafe(t *testing.T) {
	artifactDir := t.TempDir()
	privateDir := t.TempDir()
	privateCommandDir := filepath.Join(privateDir, "private-command")
	if err := os.MkdirAll(privateCommandDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const secret = "private-endpoint.example.invalid"
	stderrName := "004-connect-proxy-only-explicit.stderr"
	if err := os.WriteFile(filepath.Join(privateCommandDir, stderrName), []byte("podlaz: authorization denied: polkit denied "+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(privateCommandDir, "failed-command"), []byte(stderrName+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runRemnawaveFailureFinalizer(t, artifactDir, privateDir, "diagnostic_unknown", "proxy.data_plane", false)
	if err != nil {
		t.Fatalf("finalize failure evidence: %v\nstdout=%s\nstderr=%s", err, stdout, stderr)
	}
	result, err := os.ReadFile(filepath.Join(artifactDir, "remnawave-proxy.txt"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(result)
	for _, want := range []string{"failure.class=capability\n", "failure.step=connect-proxy-only-explicit\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("sanitized failure report lost %q: %q", want, text)
		}
	}
	if strings.Contains(text, secret) || strings.Contains(stdout, secret) || strings.Contains(stderr, secret) {
		t.Fatal("private value leaked through Remnawave failure evidence")
	}
	if scanOut, scanErr, err := runRemnawaveArtifactScanner(t, "proxy", artifactDir); err != nil {
		t.Fatalf("sanitized failure report rejected: %v\nstdout=%s\nstderr=%s", err, scanOut, scanErr)
	}
}

func TestRemnawaveFailureEvidenceUsesGenericStepWithoutPrivateCommandEvidence(t *testing.T) {
	artifactDir := t.TempDir()
	privateDir := t.TempDir()
	stdout, stderr, err := runRemnawaveFailureFinalizer(t, artifactDir, privateDir, "diagnostic_unknown", "proxy.data_plane", false)
	if err != nil {
		t.Fatalf("finalize generic failure evidence: %v\nstdout=%s\nstderr=%s", err, stdout, stderr)
	}
	result, err := os.ReadFile(filepath.Join(artifactDir, "remnawave-proxy.txt"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(result)
	if !strings.Contains(text, "failure.class=diagnostic_unknown\n") || !strings.Contains(text, "failure.step=proxy.data_plane\n") {
		t.Fatalf("unexpected generic failure evidence: %q", text)
	}
}

func TestRemnawaveFailureEvidenceFinalizationIsIdempotent(t *testing.T) {
	artifactDir := t.TempDir()
	privateDir := t.TempDir()
	privateCommandDir := filepath.Join(privateDir, "private-command")
	if err := os.MkdirAll(privateCommandDir, 0o700); err != nil {
		t.Fatal(err)
	}
	stderrName := "004-connect-proxy-only-explicit.stderr"
	if err := os.WriteFile(filepath.Join(privateCommandDir, stderrName), []byte("daemon is unavailable\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(privateCommandDir, "failed-command"), []byte(stderrName+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if stdout, stderr, err := runRemnawaveFailureFinalizer(t, artifactDir, privateDir, "diagnostic_unknown", "proxy.data_plane", true); err != nil {
		t.Fatalf("idempotent finalization failed: %v\nstdout=%s\nstderr=%s", err, stdout, stderr)
	}
	result, err := os.ReadFile(filepath.Join(artifactDir, "remnawave-proxy.txt"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(result)
	for _, want := range []string{"failure.class=infrastructure\n", "failure.step=connect-proxy-only-explicit\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("idempotent failure evidence lost %q: %q", want, text)
		}
	}
	if strings.Contains(text, "should.not.replace") {
		t.Fatal("second finalization rewrote already-finalized failure evidence")
	}
	if strings.Count(text, "failure.class=") != 1 || strings.Count(text, "failure.step=") != 1 {
		t.Fatalf("finalized report is not idempotent: %q", text)
	}
}

func TestRemnawaveTUNPublicScannerAcceptsNormalizedFailureAndRejectsRawEvidence(t *testing.T) {
	dir := t.TempDir()
	report := filepath.Join(dir, "hosted-remnawave-tun.txt")
	keys := []string{
		"candidate.provenance",
		"remnawave.material_private",
		"native.schema_opaque",
		"native.multi_outbound",
		"ordinary_user.boundary",
		"tun.verified_active",
		"tun.system_dns",
		"tun.ipv4_tcp",
		"tun.tls",
		"tun.https",
		"tun.remnawave_path",
		"tun.doctor",
		"tun.reconnect",
		"privacy.direct_uplink_blocked",
		"foreign.state_preserved",
		"tun.clean_disconnect",
		"tun.terminal_cleanup",
		"tun.recovery_clean",
		"guest.baseline_restored",
		"guest.ordinary_connectivity_restored",
		"outer.cleanup",
		"fixture.cleanup",
	}
	write := func(failed bool) {
		t.Helper()
		var out strings.Builder
		out.WriteString("candidate.commit=" + strings.Repeat("a", 40) + "\n")
		out.WriteString("candidate.package_sha256=" + strings.Repeat("b", 64) + "\n")
		for _, key := range keys {
			value := "pass"
			if key == "tun.doctor" {
				value = "observed"
			}
			if failed && key == "fixture.cleanup" {
				value = "fail"
			}
			out.WriteString(key + "=" + value + "\n")
		}
		if failed {
			out.WriteString("failure.class=fixture\nfailure.step=fixture.cleanup\n")
		} else {
			out.WriteString("failure.class=none\nfailure.step=none\n")
		}
		if err := os.WriteFile(report, []byte(out.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(mode string) error {
		t.Helper()
		cmd := exec.Command("bash", "hosted-remnawave-tun.sh", mode)
		cmd.Env = append(os.Environ(),
			"E2E_ARTIFACT_DIR="+dir,
			"PODLAZ_E2E_CANDIDATE_COMMIT="+strings.Repeat("a", 40),
			"PODLAZ_E2E_CANDIDATE_SHA256="+strings.Repeat("b", 64),
		)
		return cmd.Run()
	}

	write(false)
	if err := run("scan-report"); err != nil {
		t.Fatalf("normalized successful Remnawave TUN report was rejected: %v", err)
	}
	if err := run("validate-report"); err != nil {
		t.Fatalf("successful Remnawave TUN verdict was rejected: %v", err)
	}

	write(true)
	if err := run("scan-report"); err != nil {
		t.Fatalf("normalized failed Remnawave TUN report was rejected for publication: %v", err)
	}
	if err := run("validate-report"); err == nil {
		t.Fatal("failed Remnawave TUN report was accepted as a successful qualification")
	}

	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(report, append(data, []byte("unexpected.raw=value\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run("scan-report"); err == nil {
		t.Fatal("Remnawave TUN public scanner accepted non-normalized evidence")
	}
}

func TestRemnawaveWorkflowsKeepQualificationFailureAndSafePublicationSeparate(t *testing.T) {
	for _, workflow := range []string{
		"../../.github/workflows/ephemeral-remnawave.yml",
		"../../.github/workflows/integration.yml",
		"../../.github/workflows/release.yml",
	} {
		data, err := os.ReadFile(workflow)
		if err != nil {
			t.Fatalf("read %s: %v", workflow, err)
		}
		text := string(data)
		for _, required := range []string{
			"id: qualification",
			"id: evidence_scan",
			"id: private_cleanup",
			"steps.evidence_scan.outcome == 'success'",
			"steps.private_cleanup.outcome == 'success'",
		} {
			if !strings.Contains(text, required) {
				t.Fatalf("%s lost fail-closed Remnawave publication contract %q", workflow, required)
			}
		}
		if strings.Contains(text, "steps.qualification.outcome == 'success'") {
			t.Fatalf("%s must allow sanitized failure evidence upload without converting qualification failure into success", workflow)
		}
	}

	releaseData, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(releaseData), "- remnawave-proxy") {
		t.Fatal("release publication no longer depends on Remnawave qualification")
	}
}

func TestRemnawaveWorkflowScansPublicEvidenceBeforePrivateCleanup(t *testing.T) {
	for _, workflow := range []string{
		"../../.github/workflows/ephemeral-remnawave.yml",
		"../../.github/workflows/integration.yml",
		"../../.github/workflows/release.yml",
	} {
		data, err := os.ReadFile(workflow)
		if err != nil {
			t.Fatalf("read %s: %v", workflow, err)
		}
		text := string(data)
		searchFrom := 0
		found := 0
		for {
			scanRel := strings.Index(text[searchFrom:], "id: evidence_scan")
			if scanRel < 0 {
				break
			}
			scan := searchFrom + scanRel
			cleanupRel := strings.Index(text[scan:], "id: private_cleanup")
			if cleanupRel < 0 {
				t.Fatalf("%s has evidence scan without subsequent private cleanup", workflow)
			}
			cleanup := scan + cleanupRel
			nextQualificationRel := strings.Index(text[scan+1:], "id: qualification")
			if nextQualificationRel >= 0 && scan+1+nextQualificationRel < cleanup {
				t.Fatalf("%s crosses job boundaries while matching evidence scan to private cleanup", workflow)
			}
			if scan >= cleanup {
				t.Fatalf("%s must scan sanitized public evidence before private cleanup", workflow)
			}
			found++
			searchFrom = cleanup + len("id: private_cleanup")
		}
		if found == 0 {
			t.Fatalf("%s has no Remnawave evidence scan/private cleanup pair", workflow)
		}
	}
}

func TestPermanentRemnawaveWorkflowsDoNotConsumeMaintainerProviderSecrets(t *testing.T) {
	for _, path := range []string{
		"../../.github/workflows/integration.yml",
		"../../.github/workflows/release.yml",
		"../../.github/workflows/hosted-package-restart.yml",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, forbidden := range []string{
			"environment: vpn-e2e",
			"${{ secrets.PODLAZ_E2E_PROFILE_URI }}",
			"${{ secrets.PODLAZ_E2E_PROFILE_URI_LIST }}",
			"${{ secrets.PODLAZ_E2E_EXPECTED_EGRESS_IP }}",
		} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s still consumes retired maintainer provider state %q", path, forbidden)
			}
		}
	}
}

func TestEphemeralRemnawaveWorkflowPinsCandidateToCheckoutAndCoversHarness(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/ephemeral-remnawave.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{
		"scripts/e2e/data-plane.sh",
		"scripts/e2e/scan-remnawave-artifacts.sh",
		"CANDIDATE_COMMIT: ${{ github.sha }}",
		"git rev-parse HEAD",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("ephemeral Remnawave workflow lost %q", required)
		}
	}
	if strings.Contains(text, "github.event.pull_request.head.sha || github.sha") {
		t.Fatal("ephemeral Remnawave workflow mixes PR-head provenance with merge checkout")
	}
}

func TestRemnawaveFixturePollingAndVersionHardening(t *testing.T) {
	data, err := os.ReadFile("remnawave-fixture.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{"api_try()", "api_try GET", "mask_value \"${NODE_SECRET}\"", "remnawave_versions.sh"} {
		if !strings.Contains(text, required) {
			t.Fatalf("Remnawave fixture hardening lost %q", required)
		}
	}

	versions, err := os.ReadFile("lib/remnawave_versions.sh")
	if err != nil {
		t.Fatal(err)
	}
	versionText := string(versions)
	for _, required := range []string{"3.4.5", "3.4.2", "REMNAWAVE_PANEL_VERSION}@sha256:", "REMNAWAVE_NODE_VERSION}@sha256:"} {
		if !strings.Contains(versionText, required) {
			t.Fatalf("canonical Remnawave versions lost %q", required)
		}
	}
}

func TestProxyAndSubscriptionFinalizeEvidenceAfterFixtureCleanup(t *testing.T) {
	for _, path := range []string{"remnawave-proxy-acceptance.sh", "remnawave-subscription-acceptance.sh"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		cleanup := strings.Index(text, "cleanup_fixture 0")
		record := strings.Index(text, "remnawave_record fixture.cleanup")
		finalize := strings.Index(text, "remnawave_finalize_report")
		if cleanup < 0 || record < cleanup || finalize < record {
			t.Fatalf("%s does not finalize normalized evidence after fixture cleanup", path)
		}
	}
}
