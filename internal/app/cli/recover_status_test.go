package cli

import (
	"bytes"
	"context"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/recovery"
)

func TestRecoverPlanJSONDoesNotReportOKWhenInspectionIsIncomplete(t *testing.T) {
	payload := recoverPlanJSON(recoverPlanView{PlanResult: recovery.PlanResult{Warnings: []recovery.Warning{{Target: "systemd-resolved", Message: "permission denied"}}}})
	if got := payload["status"]; got != "warn" {
		t.Fatalf("incomplete inspection must not report top-level ok, got %#v", got)
	}
}

func TestRecoverPlanJSONDoesNotReportOKWhenCleanupCandidatesRemain(t *testing.T) {
	payload := recoverPlanJSON(recoverPlanView{PlanResult: recovery.PlanResult{Candidates: []recovery.Candidate{{Kind: "dns-link", Description: "resolved link state", Target: "podlaz0"}}}})
	if got := payload["status"]; got != "warn" {
		t.Fatalf("pending cleanup must not report top-level ok, got %#v", got)
	}
}

func TestRecoverExecuteWarningsProduceWarnStatusAndNonzeroExit(t *testing.T) {
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"recover", "--execute", "--yes", "--json"}, &out, options{
		recoverExecute: func(context.Context) (recovery.ExecuteResult, error) {
			return recovery.ExecuteResult{Warnings: []recovery.Warning{{Target: "systemd-resolved", Message: "permission denied"}}}, nil
		},
	})
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("incomplete inspection must return exit 1, got err=%v code=%d", err, ExitCode(err))
	}
	if got := out.String(); !bytes.Contains([]byte(got), []byte(`"status": "warn"`)) {
		t.Fatalf("incomplete inspection must render warn JSON, got %q", got)
	}
}
