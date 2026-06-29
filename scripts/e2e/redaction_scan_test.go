package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestE2ERedactionScanPassesWhenArtifactsDoNotContainSecrets(t *testing.T) {
	artifactDir := t.TempDir()
	result := runBash(t, artifactDir, `
set -Eeuo pipefail
source ./lib/e2e.sh
printf 'profile id only\n' >"${E2E_ARTIFACT_DIR}/safe-output.txt"
assert_artifacts_do_not_contain_sensitive_values "no-leak" "vless://real-secret@example.test:443#private"
grep -F "No configured sensitive values" "${E2E_ARTIFACT_DIR}/no-leak-redaction-scan.txt" >/dev/null
`)

	if result.err != nil {
		t.Fatalf("redaction scan should pass without leaks: %v\nstdout:\n%s\nstderr:\n%s", result.err, result.stdout, result.stderr)
	}
}

func TestE2ERedactionScanFailsWithoutEchoingSecretContent(t *testing.T) {
	artifactDir := t.TempDir()
	const secret = "vless://real-secret@example.test:443#private"
	result := runBash(t, artifactDir, `
set -Eeuo pipefail
source ./lib/e2e.sh
printf 'safe prefix\n%s\nsafe suffix\n' "${SECRET_VALUE}" >"${E2E_ARTIFACT_DIR}/leaky-output.txt"
assert_artifacts_do_not_contain_sensitive_values "leak-check" "${SECRET_VALUE}"
`)

	if result.err == nil {
		t.Fatalf("redaction scan should fail when an artifact contains a configured secret")
	}
	assertNoSecretLeak(t, result.stdout, secret, "stdout")
	assertNoSecretLeak(t, result.stderr, secret, "stderr")

	reportPath := filepath.Join(artifactDir, "leak-check-redaction-scan.txt")
	report, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read redaction report: %v", err)
	}
	assertNoSecretLeak(t, string(report), secret, "redaction report")
	if !strings.Contains(string(report), "leaky-output.txt") {
		t.Fatalf("expected report to identify leaking artifact path, got %q", string(report))
	}
}

func TestE2ERedactionScanChecksEachLineOfMultilineSecrets(t *testing.T) {
	artifactDir := t.TempDir()
	const firstSecret = "trojan://first-secret@example.test:443#private"
	const secondSecret = "ss://second-secret@example.test:443#private"
	result := runBash(t, artifactDir, `
set -Eeuo pipefail
source ./lib/e2e.sh
printf 'only second configured secret appears: %s\n' "${SECOND_SECRET}" >"${E2E_ARTIFACT_DIR}/multiline-leak.txt"
SECRET_LINES="${FIRST_SECRET}"$'\n'"${SECOND_SECRET}"
assert_artifacts_do_not_contain_sensitive_values "multiline-leak" "${SECRET_LINES}"
`)

	if result.err == nil {
		t.Fatalf("redaction scan should fail when any line of a multiline configured secret appears")
	}
	assertNoSecretLeak(t, result.stdout, firstSecret, "stdout")
	assertNoSecretLeak(t, result.stdout, secondSecret, "stdout")
	assertNoSecretLeak(t, result.stderr, firstSecret, "stderr")
	assertNoSecretLeak(t, result.stderr, secondSecret, "stderr")

	reportPath := filepath.Join(artifactDir, "multiline-leak-redaction-scan.txt")
	report, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read redaction report: %v", err)
	}
	assertNoSecretLeak(t, string(report), firstSecret, "redaction report")
	assertNoSecretLeak(t, string(report), secondSecret, "redaction report")
	if !strings.Contains(string(report), "multiline-leak.txt") {
		t.Fatalf("expected report to identify leaking artifact path, got %q", string(report))
	}
}

func TestE2EScriptsHaveValidBashSyntax(t *testing.T) {
	for _, path := range []string{"lib/e2e.sh", "data-plane.sh", "coverage-evidence.sh"} {
		t.Run(path, func(t *testing.T) {
			cmd := exec.Command("bash", "-n", path)
			cmd.Dir = "."
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("bash -n %s failed: %v\n%s", path, err, output)
			}
		})
	}
}

type bashResult struct {
	stdout string
	stderr string
	err    error
}

func runBash(t *testing.T, artifactDir, script string) bashResult {
	t.Helper()
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(),
		"E2E_ARTIFACT_DIR="+artifactDir,
		"E2E_TMP_ROOT="+t.TempDir(),
		"SECRET_VALUE=vless://real-secret@example.test:443#private",
		"FIRST_SECRET=trojan://first-secret@example.test:443#private",
		"SECOND_SECRET=ss://second-secret@example.test:443#private",
	)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return bashResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func assertNoSecretLeak(t *testing.T, got, secret, label string) {
	t.Helper()
	if strings.Contains(got, secret) {
		t.Fatalf("%s leaked secret %q in %q", label, secret, got)
	}
}
