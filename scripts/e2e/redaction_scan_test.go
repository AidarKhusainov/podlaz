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

func TestE2ERedactionScanChecksDerivedUUIDAndAuthorizationToken(t *testing.T) {
	artifactDir := t.TempDir()
	const uuidSecret = "123e4567-e89b-12d3-a456-426614174000"
	const authToken = "provider-token-123456789"
	result := runBash(t, artifactDir, `
set -Eeuo pipefail
source ./lib/e2e.sh
PROFILE_URI="vless://${UUID_SECRET}@vpn.example.test:443?type=tcp&security=reality&encryption=none#private"
AUTH_HEADER="Authorization: Bearer ${AUTH_TOKEN}"
printf 'runtime uuid: %s\nheader token: %s\n' "${UUID_SECRET}" "${AUTH_TOKEN}" >"${E2E_ARTIFACT_DIR}/derived-fragment-leak.txt"
assert_artifacts_do_not_contain_sensitive_values "derived-fragment-leak" "${PROFILE_URI}" "${AUTH_HEADER}"
`)

	if result.err == nil {
		t.Fatalf("redaction scan should fail when artifacts contain derived UUID/token fragments")
	}
	assertNoSecretLeak(t, result.stdout, uuidSecret, "stdout")
	assertNoSecretLeak(t, result.stdout, authToken, "stdout")
	assertNoSecretLeak(t, result.stderr, uuidSecret, "stderr")
	assertNoSecretLeak(t, result.stderr, authToken, "stderr")

	reportPath := filepath.Join(artifactDir, "derived-fragment-leak-redaction-scan.txt")
	report, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read redaction report: %v", err)
	}
	assertNoSecretLeak(t, string(report), uuidSecret, "redaction report")
	assertNoSecretLeak(t, string(report), authToken, "redaction report")
	if !strings.Contains(string(report), "derived-fragment-leak.txt") {
		t.Fatalf("expected report to identify leaking artifact path, got %q", string(report))
	}
}

func TestE2ERedactionScanChecksDerivedSubscriptionProviderSecrets(t *testing.T) {
	artifactDir := t.TempDir()
	secrets := []string{
		"provider-token-123456789",
		"provider-token-abcdefghi",
		"query-token-123456789",
		"userinfo-token-123456789",
	}
	result := runBash(t, artifactDir, `
set -Eeuo pipefail
source ./lib/e2e.sh
SUBSCRIPTION_PATH_URL="https://provider.example/sub/${AUTH_TOKEN}"
SUBSCRIPTION_DECODED_PATH_URL="https://provider.example/sub/provider-token%2Dabcdefghi"
SUBSCRIPTION_QUERY_URL="https://provider.example/api/v1?access_token=query-token-123456789"
SUBSCRIPTION_USERINFO_URL="https://provider-user:userinfo-token-123456789@provider.example/sub"
printf 'subscription path token: %s\n' "${AUTH_TOKEN}" >"${E2E_ARTIFACT_DIR}/subscription-path-token-leak.txt"
printf 'subscription decoded path token: provider-token-abcdefghi\n' >"${E2E_ARTIFACT_DIR}/subscription-decoded-path-token-leak.txt"
printf 'subscription query token: query-token-123456789\n' >"${E2E_ARTIFACT_DIR}/subscription-query-token-leak.txt"
printf 'subscription userinfo token: userinfo-token-123456789\n' >"${E2E_ARTIFACT_DIR}/subscription-userinfo-token-leak.txt"
assert_artifacts_do_not_contain_sensitive_values "subscription-provider-secret-leak" \
  "${SUBSCRIPTION_PATH_URL}" \
  "${SUBSCRIPTION_DECODED_PATH_URL}" \
  "${SUBSCRIPTION_QUERY_URL}" \
  "${SUBSCRIPTION_USERINFO_URL}"
`)

	if result.err == nil {
		t.Fatalf("redaction scan should fail when artifacts contain tokens derived from configured subscription URLs")
	}
	for _, secret := range secrets {
		assertNoSecretLeak(t, result.stdout, secret, "stdout")
		assertNoSecretLeak(t, result.stderr, secret, "stderr")
	}

	reportPath := filepath.Join(artifactDir, "subscription-provider-secret-leak-redaction-scan.txt")
	report, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read redaction report: %v", err)
	}
	for _, secret := range secrets {
		assertNoSecretLeak(t, string(report), secret, "redaction report")
	}
	for _, filename := range []string{
		"subscription-path-token-leak.txt",
		"subscription-decoded-path-token-leak.txt",
		"subscription-query-token-leak.txt",
		"subscription-userinfo-token-leak.txt",
	} {
		if !strings.Contains(string(report), filename) {
			t.Fatalf("expected report to identify leaking artifact path %q, got %q", filename, string(report))
		}
	}
}

func TestE2EGeneratedContentScanDetectsFullRuntimeConfigLeak(t *testing.T) {
	artifactDir := t.TempDir()
	const runtimeSecret = "123e4567-e89b-12d3-a456-426614174001"
	result := runBash(t, artifactDir, `
set -Eeuo pipefail
source ./lib/e2e.sh
runtime_config="${E2E_TMP_ROOT}/xray-runtime.json"
cat >"${runtime_config}" <<JSON
{"log":{"loglevel":"warning"},"outbounds":[{"protocol":"vless","settings":{"vnext":[{"address":"vpn.example.test","port":443,"users":[{"id":"${RUNTIME_SECRET}","encryption":"none"}]}]}}]}
JSON
printf '{\n}\n' >"${E2E_ARTIFACT_DIR}/common-json-shape.txt"
assert_artifacts_do_not_contain_file_contents "runtime-safe" "${runtime_config}"
cat "${runtime_config}" >"${E2E_ARTIFACT_DIR}/runtime-leak.txt"
assert_artifacts_do_not_contain_file_contents "runtime-leak" "${runtime_config}"
`)

	if result.err == nil {
		t.Fatalf("generated-content scan should fail when an artifact contains the full runtime config")
	}
	assertNoSecretLeak(t, result.stdout, runtimeSecret, "stdout")
	assertNoSecretLeak(t, result.stderr, runtimeSecret, "stderr")

	reportPath := filepath.Join(artifactDir, "runtime-leak-content-redaction-scan.txt")
	report, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read generated-content report: %v", err)
	}
	assertNoSecretLeak(t, string(report), runtimeSecret, "generated-content report")
	if !strings.Contains(string(report), "runtime-leak.txt") {
		t.Fatalf("expected report to identify leaking artifact path, got %q", string(report))
	}
}

func TestE2EScriptsHaveValidBashSyntax(t *testing.T) {
	for _, path := range []string{"lib/e2e.sh", "data-plane.sh", "server-coverage.sh", "coverage-evidence.sh"} {
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
		"UUID_SECRET=123e4567-e89b-12d3-a456-426614174000",
		"AUTH_TOKEN=provider-token-123456789",
		"RUNTIME_SECRET=123e4567-e89b-12d3-a456-426614174001",
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
