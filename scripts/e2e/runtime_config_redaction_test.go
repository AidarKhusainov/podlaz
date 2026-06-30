package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestE2EActiveRuntimeConfigScanUsesStatusRuntimeConfigPath(t *testing.T) {
	artifactDir := t.TempDir()
	const privateValue = "123e4567-e89b-12d3-a456-426614174002"
	result := runRuntimeConfigBash(t, artifactDir, `
set -Eeuo pipefail
source ./lib/e2e.sh
PRIVATE_VALUE="123e4567-e89b-12d3-a456-426614174002"
runtime_config="${E2E_TMP_ROOT}/xray-runtime.json"
status_json="${E2E_TMP_ROOT}/active-status.json"
cat >"${runtime_config}" <<JSON
{"log":{"loglevel":"warning"},"outbounds":[{"tag":"primary","protocol":"freedom","settings":{"private_id":"${PRIVATE_VALUE}","padding":"long-enough-generated-runtime-config-fixture"}}]}
JSON
python3 - "${runtime_config}" "${status_json}" <<'PY'
import json
import sys
with open(sys.argv[2], "w", encoding="utf-8") as handle:
    json.dump({"daemon": "running", "connection": "active", "runtime_config_path": sys.argv[1]}, handle)
PY
printf 'status output may contain only runtime config path: %s\n' "${runtime_config}" >"${E2E_ARTIFACT_DIR}/status-path-only.txt"
assert_active_runtime_config_artifacts_safe "runtime-config-safe" "${status_json}"
cat "${runtime_config}" >"${E2E_ARTIFACT_DIR}/runtime-config-leak.txt"
assert_active_runtime_config_artifacts_safe "runtime-config-leak" "${status_json}"
`)

	if result.err == nil {
		t.Fatalf("active runtime config scan should fail when artifacts contain the full runtime config")
	}
	assertRuntimeConfigValueNotLeaked(t, result.stdout, privateValue, "stdout")
	assertRuntimeConfigValueNotLeaked(t, result.stderr, privateValue, "stderr")

	reportPath := filepath.Join(artifactDir, "runtime-config-leak-content-redaction-scan.txt")
	report, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read active runtime-config report: %v", err)
	}
	assertRuntimeConfigValueNotLeaked(t, string(report), privateValue, "generated-content report")
	if !strings.Contains(string(report), "runtime-config-leak.txt") {
		t.Fatalf("expected report to identify leaking artifact path, got %q", string(report))
	}
}

type runtimeConfigBashResult struct {
	stdout string
	stderr string
	err    error
}

func runRuntimeConfigBash(t *testing.T, artifactDir, script string) runtimeConfigBashResult {
	t.Helper()
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(),
		"E2E_ARTIFACT_DIR="+artifactDir,
		"E2E_TMP_ROOT="+t.TempDir(),
	)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return runtimeConfigBashResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func assertRuntimeConfigValueNotLeaked(t *testing.T, got, value, label string) {
	t.Helper()
	if strings.Contains(got, value) {
		t.Fatalf("%s leaked private runtime config value %q in %q", label, value, got)
	}
}
