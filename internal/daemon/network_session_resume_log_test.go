package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestNetworkSessionResumeDiagnosticStoreUsesPrivatePermissions(t *testing.T) {
	store := newNetworkSessionResumeDiagnosticStore(t.TempDir(), fixedBootID("boot-a"))
	if err := store.Save(networkSessionResumeDiagnostic{
		ResumeStage:       api.NetworkSessionResumeStageConnectReplay,
		LastResumeOutcome: api.NetworkSessionResumeOutcomeFailed,
		TUNFailurePhase:   "preflight",
		RollbackStatus:    "not-started",
	}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(store.path())
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("resume diagnostic mode=%o want=600", got)
	}
}

func TestNetworkSessionResumeDiagnosticExtendedV1RemainsReadableByReleasedProjection(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionResumeDiagnosticStore(runtimeDir, fixedBootID("boot-a"))
	attempt := &networkSessionReplayAttempt{
		SessionID:            strings.Repeat("a", 32),
		RecoveryEpoch:        4,
		ReplayDisposition:    networkSessionReplayDispositionTerminal,
		ResumeStage:          api.NetworkSessionResumeStageConnectReplay,
		TUNFailurePhase:      "network-apply",
		NetworkApplySubphase: "dns",
		RollbackStatus:       "completed",
		TransactionPresent:   true,
		CandidateMutation:    networkSessionCandidateMutationRolledBack,
	}
	if err := store.Save(networkSessionResumeDiagnostic{
		RecoveryEpoch:        attempt.RecoveryEpoch,
		ResumeStage:          attempt.ResumeStage,
		LastResumeOutcome:    api.NetworkSessionResumeOutcomeFailed,
		TUNFailurePhase:      attempt.TUNFailurePhase,
		RollbackStatus:       attempt.RollbackStatus,
		TransactionPresent:   attempt.TransactionPresent,
		ReplayDisposition:    string(attempt.ReplayDisposition),
		NetworkApplySubphase: attempt.NetworkApplySubphase,
		Originating:          attempt,
		Current:              attempt,
	}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(store.path())
	if err != nil {
		t.Fatal(err)
	}
	var released struct {
		SchemaVersion      string `json:"schema_version"`
		Owner              string `json:"owner"`
		BootID             string `json:"boot_id"`
		RecoveryEpoch      uint64 `json:"recovery_epoch"`
		ResumeStage        string `json:"resume_stage"`
		LastResumeOutcome  string `json:"last_resume_outcome"`
		TUNFailurePhase    string `json:"tun_failure_phase,omitempty"`
		RollbackStatus     string `json:"rollback_status,omitempty"`
		TransactionPresent bool   `json:"transaction_present"`
		LegacyMigration    bool   `json:"legacy_migration"`
	}
	if err := json.Unmarshal(data, &released); err != nil {
		t.Fatalf("released v1 projection cannot decode extended record: %v", err)
	}
	if released.SchemaVersion != networkSessionResumeDiagnosticSchemaVersion || released.Owner != networkSessionResumeDiagnosticOwner || released.BootID != "boot-a" {
		t.Fatalf("released projection identity changed: %#v", released)
	}
	if released.RecoveryEpoch != 4 || released.ResumeStage != api.NetworkSessionResumeStageConnectReplay || released.TUNFailurePhase != "network-apply" || released.RollbackStatus != "completed" || !released.TransactionPresent {
		t.Fatalf("released projection changed: %#v", released)
	}
}

func TestPreReplayBlockerKeepsStructuredReplayEvidenceAndClearsStaleTopLevelReplayFields(t *testing.T) {
	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	store := newNetworkSessionResumeDiagnosticStore(runtimeDir, fixedBootID("boot-a"))
	attempt := &networkSessionReplayAttempt{
		SessionID:            strings.Repeat("b", 32),
		RecoveryEpoch:        7,
		ReplayDisposition:    networkSessionReplayDispositionTerminal,
		ResumeStage:          api.NetworkSessionResumeStageConnectReplay,
		TUNFailurePhase:      "network-apply",
		NetworkApplySubphase: "dns",
		RollbackStatus:       "completed",
		TransactionPresent:   true,
		CandidateMutation:    networkSessionCandidateMutationRolledBack,
	}
	if err := store.Save(networkSessionResumeDiagnostic{
		RecoveryEpoch:        attempt.RecoveryEpoch,
		ResumeStage:          attempt.ResumeStage,
		LastResumeOutcome:    api.NetworkSessionResumeOutcomeFailed,
		TUNFailurePhase:      attempt.TUNFailurePhase,
		RollbackStatus:       attempt.RollbackStatus,
		TransactionPresent:   attempt.TransactionPresent,
		ReplayDisposition:    string(attempt.ReplayDisposition),
		NetworkApplySubphase: attempt.NetworkApplySubphase,
		Originating:          attempt,
		Current:              attempt,
	}); err != nil {
		t.Fatal(err)
	}

	blocker := newNetworkSessionResumeOutcomeError(
		api.NetworkSessionResumeStageExactRecovery,
		api.NetworkSessionResumeOutcomeIncomplete,
		false,
		false,
		errors.New("exact recovery remains incomplete"),
	)
	_ = persistNetworkSessionResumeFailure(continuation, attempt.RecoveryEpoch, blocker)

	record, exists, err := store.Load()
	if err != nil || !exists {
		t.Fatalf("load updated diagnostic: exists=%v err=%v", exists, err)
	}
	if record.ResumeStage != api.NetworkSessionResumeStageExactRecovery || record.LastResumeOutcome != api.NetworkSessionResumeOutcomeIncomplete {
		t.Fatalf("top-level latest blocker not updated: %#v", record)
	}
	if record.ReplayDisposition != "" || record.NetworkApplySubphase != "" {
		t.Fatalf("top-level replay-only fields leaked from older blocker: disposition=%q subphase=%q", record.ReplayDisposition, record.NetworkApplySubphase)
	}
	if record.Originating == nil || record.Current == nil {
		t.Fatalf("structured replay evidence was erased: %#v", record)
	}
	if record.Current.RecoveryEpoch != attempt.RecoveryEpoch || record.Current.ReplayDisposition != networkSessionReplayDispositionTerminal || record.Current.NetworkApplySubphase != "dns" {
		t.Fatalf("structured current changed with pre-replay blocker: %#v", record.Current)
	}
}

func TestNetworkSessionResumeFailureLogNeverIncludesNestedErrorText(t *testing.T) {
	var output bytes.Buffer
	originalOutput := log.Writer()
	originalFlags := log.Flags()
	originalPrefix := log.Prefix()
	log.SetOutput(&output)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(originalOutput)
		log.SetFlags(originalFlags)
		log.SetPrefix(originalPrefix)
	})

	secret := "private-endpoint.example.test private-token-value profile-private-value"
	err := newNetworkSessionResumeError(
		api.NetworkSessionResumeStageConnectReplay,
		false,
		withTunFailurePhase("preflight", noTunTransactionID, "not-started", errors.New(secret)),
	)
	logNetworkSessionResumeFailure(err)

	line := output.String()
	for _, want := range []string{
		"event=network_session_resume_failed",
		"resume_stage=connect-replay",
		"tun_failure_phase=preflight",
		"rollback_status=not-started",
		"transaction_present=false",
		"startup_gate=blocked",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("missing %q in resume log: %s", want, line)
		}
	}
	for _, forbidden := range strings.Fields(secret) {
		if strings.Contains(line, forbidden) {
			t.Fatalf("resume log leaked %q: %s", forbidden, line)
		}
	}
}
