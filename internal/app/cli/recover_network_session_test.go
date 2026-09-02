package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
)

func TestRecoverDryRunShowsRetainedNetworkSessionAuthority(t *testing.T) {
	state := &api.NetworkSessionRecoveryState{
		Authority:           api.NetworkSessionRecoveryAuthorityPresent,
		Intent:              "resume",
		StartupGate:         api.NetworkSessionStartupGateBlocked,
		ResumeStage:         api.NetworkSessionResumeStageConnectReplay,
		LastResumeOutcome:   api.NetworkSessionResumeOutcomeFailed,
		LastTUNFailurePhase: "preflight",
		RollbackStatus:      "not-started",
		CleanupAuthority:    api.NetworkSessionCleanupAuthorityNone,
		NextAction:          api.NetworkSessionRecoveryActionRetryResume,
	}
	var out bytes.Buffer
	err := runRecoverCommand(context.Background(), nil, &out, options{
		recover: func(context.Context) recovery.PlanResult {
			return recovery.PlanResult{NetworkSession: state}
		},
	})
	if err != nil {
		t.Fatalf("recover dry-run: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "No podlaz-owned recovery candidates found") {
		t.Fatalf("retained Network Session was reported as no recovery work: %q", got)
	}
	for _, want := range []string{
		"Network Session authority: present",
		"Intent: resume",
		"Startup gate: blocked",
		"Resume stage: connect-replay",
		"Last resume outcome: failed",
		"TUN failure phase: preflight",
		"Cleanup authority: none",
		"Next action: retry-resume",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("recover dry-run missing %q: %q", want, got)
		}
	}
}

func TestRecoverExecuteFailedResumeUsesSameSemanticModelAndFails(t *testing.T) {
	state := &api.NetworkSessionRecoveryState{
		Authority:           api.NetworkSessionRecoveryAuthorityPresent,
		Intent:              "resume",
		StartupGate:         api.NetworkSessionStartupGateBlocked,
		ResumeStage:         api.NetworkSessionResumeStageConnectReplay,
		LastResumeOutcome:   api.NetworkSessionResumeOutcomeFailed,
		LastTUNFailurePhase: "preflight",
		RollbackStatus:      "not-started",
		CleanupAuthority:    api.NetworkSessionCleanupAuthorityNone,
		NextAction:          api.NetworkSessionRecoveryActionRetryResume,
	}
	var out bytes.Buffer
	err := runRecoverCommand(context.Background(), []string{"--execute", "--yes"}, &out, options{
		recoverExecute: func(context.Context) (recovery.ExecuteResult, error) {
			return recovery.ExecuteResult{NetworkSession: state}, nil
		},
	})
	var exit exitError
	if !errors.As(err, &exit) || exit.code != 1 {
		t.Fatalf("failed resume exit=%v, want exit 1", err)
	}
	got := out.String()
	for _, want := range []string{"Network Session authority: present", "Next action: retry-resume"} {
		if !strings.Contains(got, want) {
			t.Fatalf("recover execute missing %q: %q", want, got)
		}
	}
}

func TestRecoverExecuteSuccessfulResumeReturnsSuccess(t *testing.T) {
	state := &api.NetworkSessionRecoveryState{
		Authority:         api.NetworkSessionRecoveryAuthorityPresent,
		Intent:            "resume",
		StartupGate:       api.NetworkSessionStartupGateOpen,
		LastResumeOutcome: api.NetworkSessionResumeOutcomeSucceeded,
		CleanupAuthority:  api.NetworkSessionCleanupAuthorityNone,
		NextAction:        api.NetworkSessionRecoveryActionNone,
	}
	var out bytes.Buffer
	err := runRecoverCommand(context.Background(), []string{"--execute", "--yes"}, &out, options{
		recoverExecute: func(context.Context) (recovery.ExecuteResult, error) {
			return recovery.ExecuteResult{NetworkSession: state}, nil
		},
	})
	if err != nil {
		t.Fatalf("successful recovery resume: %v", err)
	}
}
