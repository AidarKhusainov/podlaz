package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

type networkSessionAuthoritySnapshot struct {
	intent            networkSessionIntent
	protectionPresent bool
	legacyMigration   bool
	recoveryEpoch     uint64
}

func inspectNetworkSessionRecoveryPlan(
	continuation networkSessionContinuationStore,
	gate *networkSessionStartupMutationGate,
) (*api.NetworkSessionRecoveryState, error) {
	if gate == nil || !gate.Blocked() {
		return nil, nil
	}

	authority, exists, err := inspectCurrentBootNetworkSessionAuthority(continuation)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}

	plan := &api.NetworkSessionRecoveryState{
		Authority:         api.NetworkSessionRecoveryAuthorityPresent,
		Intent:            string(authority.intent),
		StartupGate:       api.NetworkSessionStartupGateBlocked,
		LastResumeOutcome: api.NetworkSessionResumeOutcomeNotAttempted,
		LegacyMigration:   authority.legacyMigration,
		CleanupAuthority:  api.NetworkSessionCleanupAuthorityNone,
	}
	if authority.protectionPresent {
		plan.CleanupAuthority = api.NetworkSessionCleanupAuthoritySessionProtection
	}

	if diagnostic, diagnosticExists, diagnosticErr := newNetworkSessionResumeDiagnosticStore(continuation.runtimeDir, continuation.readBootID).Load(); diagnosticErr != nil {
		return nil, diagnosticErr
	} else if diagnosticExists && diagnostic.RecoveryEpoch == authority.recoveryEpoch {
		plan.ResumeStage = diagnostic.ResumeStage
		plan.LastResumeOutcome = diagnostic.LastResumeOutcome
		plan.LastTUNFailurePhase = diagnostic.TUNFailurePhase
		plan.RollbackStatus = diagnostic.RollbackStatus
		plan.TransactionPresent = diagnostic.TransactionPresent
		plan.LegacyMigration = plan.LegacyMigration || diagnostic.LegacyMigration
	}

	switch authority.intent {
	case networkSessionIntentResume:
		plan.NextAction = api.NetworkSessionRecoveryActionRetryResume
	case networkSessionIntentDisconnect, networkSessionIntentTerminal:
		plan.NextAction = api.NetworkSessionRecoveryActionContinueTeardown
	default:
		plan.NextAction = api.NetworkSessionRecoveryActionManualDiagnosis
	}
	if err := api.ValidateNetworkSessionRecoveryState(*plan); err != nil {
		return nil, fmt.Errorf("validate network session recovery plan: %w", err)
	}
	return plan, nil
}

// inspectCurrentBootNetworkSessionAuthority is intentionally mutation-free. In
// particular it must not call networkSessionStateStore.Load because that method
// is allowed to migrate legacy records and discard previous-boot state as part
// of normal startup convergence.
func inspectCurrentBootNetworkSessionAuthority(continuation networkSessionContinuationStore) (networkSessionAuthoritySnapshot, bool, error) {
	file, err := os.Open(continuation.path())
	if errors.Is(err, os.ErrNotExist) {
		return networkSessionAuthoritySnapshot{}, false, nil
	}
	if err != nil {
		return networkSessionAuthoritySnapshot{}, false, fmt.Errorf("open network session authority: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return networkSessionAuthoritySnapshot{}, false, fmt.Errorf("stat network session authority: %w", err)
	}
	if info.Mode().Perm() != 0o600 {
		return networkSessionAuthoritySnapshot{}, false, fmt.Errorf("network session authority permissions are %o, want 600", info.Mode().Perm())
	}
	if info.Size() > maxNetworkSessionStateBytes {
		return networkSessionAuthoritySnapshot{}, false, fmt.Errorf("network session authority exceeds %d bytes", maxNetworkSessionStateBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxNetworkSessionStateBytes+1))
	if err != nil {
		return networkSessionAuthoritySnapshot{}, false, fmt.Errorf("read network session authority: %w", err)
	}
	if len(data) > maxNetworkSessionStateBytes {
		return networkSessionAuthoritySnapshot{}, false, fmt.Errorf("network session authority exceeds %d bytes", maxNetworkSessionStateBytes)
	}

	var header struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return networkSessionAuthoritySnapshot{}, false, fmt.Errorf("decode network session authority header: %w", err)
	}
	bootID, err := continuation.currentBootID()
	if err != nil {
		return networkSessionAuthoritySnapshot{}, false, err
	}

	switch header.SchemaVersion {
	case networkSessionStateSchemaVersion:
		var state networkSessionState
		if err := json.Unmarshal(data, &state); err != nil {
			return networkSessionAuthoritySnapshot{}, false, fmt.Errorf("decode network session state for recovery inspection: %w", err)
		}
		if err := validateNetworkSessionState(state); err != nil {
			return networkSessionAuthoritySnapshot{}, false, err
		}
		if strings.TrimSpace(state.BootID) != bootID {
			return networkSessionAuthoritySnapshot{}, false, nil
		}
		return networkSessionAuthoritySnapshot{
			intent:            state.Intent,
			protectionPresent: state.Protection != nil,
			recoveryEpoch:     state.RecoveryEpoch,
		}, true, nil

	case networkSessionContinuationSchemaVersion:
		var record networkSessionContinuation
		if err := json.Unmarshal(data, &record); err != nil {
			return networkSessionAuthoritySnapshot{}, false, fmt.Errorf("decode legacy network session continuation for recovery inspection: %w", err)
		}
		if err := validateNetworkSessionContinuation(record); err != nil {
			return networkSessionAuthoritySnapshot{}, false, err
		}
		if strings.TrimSpace(record.BootID) != bootID {
			return networkSessionAuthoritySnapshot{}, false, nil
		}
		return networkSessionAuthoritySnapshot{
			intent:          networkSessionIntentResume,
			legacyMigration: true,
		}, true, nil

	default:
		return networkSessionAuthoritySnapshot{}, false, fmt.Errorf("unsupported network session authority schema %q", header.SchemaVersion)
	}
}

func successfulNetworkSessionRecoveryState(plan *api.NetworkSessionRecoveryState) *api.NetworkSessionRecoveryState {
	if plan == nil {
		return nil
	}
	out := api.CloneNetworkSessionRecoveryState(plan)
	out.StartupGate = api.NetworkSessionStartupGateOpen
	out.LastResumeOutcome = api.NetworkSessionResumeOutcomeSucceeded
	out.ResumeStage = ""
	out.LastTUNFailurePhase = ""
	out.RollbackStatus = ""
	out.TransactionPresent = false
	out.NextAction = api.NetworkSessionRecoveryActionNone
	return out
}

func failedNetworkSessionRecoveryState(plan *api.NetworkSessionRecoveryState, resumeErr error) *api.NetworkSessionRecoveryState {
	if plan == nil {
		return nil
	}
	out := api.CloneNetworkSessionRecoveryState(plan)
	out.StartupGate = api.NetworkSessionStartupGateBlocked
	out.NextAction = api.NetworkSessionRecoveryActionManualDiagnosis
	if out.Intent == string(networkSessionIntentResume) {
		out.NextAction = api.NetworkSessionRecoveryActionRetryResume
	}
	if failure, ok := networkSessionResumeFailure(resumeErr); ok {
		out.ResumeStage = failure.ResumeStage
		out.LastResumeOutcome = failure.LastResumeOutcome
		out.LastTUNFailurePhase = failure.TUNFailurePhase
		out.RollbackStatus = failure.RollbackStatus
		out.TransactionPresent = failure.TransactionPresent
		out.LegacyMigration = out.LegacyMigration || failure.LegacyMigration
	} else {
		out.LastResumeOutcome = api.NetworkSessionResumeOutcomeFailed
	}
	return out
}
