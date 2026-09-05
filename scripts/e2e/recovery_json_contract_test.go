package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runCleanRecoveryPredicate(t *testing.T, payload string) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "recover.json")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("write recovery fixture: %v", err)
	}
	cmd := exec.Command("bash", "-c", `source lib/recovery_json.sh; assert_clean_recovery_json_file "$1"`, "bash", path)
	return cmd.Run()
}

func TestCleanRecoveryJSONPredicateFailsClosed(t *testing.T) {
	valid := `{"status":"ok","warnings":[],"recovery":{"candidates":[],"warnings":[]}}`
	if err := runCleanRecoveryPredicate(t, valid); err != nil {
		t.Fatalf("clean recovery projection rejected: %v", err)
	}

	invalid := []string{
		``,
		`{}`,
		`{"status":"ok"}`,
		`{"status":"error","warnings":[],"recovery":{"candidates":[],"warnings":[]}}`,
		`{"status":"ok","warnings":["warning"],"recovery":{"candidates":[],"warnings":[]}}`,
		`{"status":"ok","warnings":[],"recovery":[]}`,
		`{"status":"ok","warnings":[],"recovery":{"candidates":[{}],"warnings":[]}}`,
		`{"status":"ok","warnings":[],"recovery":{"candidates":[],"warnings":["warning"]}}`,
	}
	for _, payload := range invalid {
		if err := runCleanRecoveryPredicate(t, payload); err == nil {
			t.Fatalf("invalid recovery projection accepted: %q", payload)
		}
	}
}

func TestRecoveryAcceptancesUseSharedPredicate(t *testing.T) {
	for _, path := range []string{
		"installed-user-lifecycle-acceptance.sh",
		"package-lifecycle-acceptance.sh",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(data)
		if !strings.Contains(text, "source \"${SCRIPT_DIR}/lib/recovery_json.sh\"") {
			t.Fatalf("%s does not source shared recovery predicate", path)
		}
		if !strings.Contains(text, "assert_clean_recovery_json_file") {
			t.Fatalf("%s does not use shared recovery predicate", path)
		}
		if strings.Contains(text, `recovery = payload.get("recovery")`) {
			t.Fatalf("%s still duplicates the recovery JSON predicate", path)
		}
	}
}
