package api

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	NetworkSessionRecoveryAuthorityPresent = "present"

	NetworkSessionStartupGateBlocked = "blocked"
	NetworkSessionStartupGateOpen    = "open"

	NetworkSessionResumeStageStateLoad        = "state-load"
	NetworkSessionResumeStageLegacyMigration  = "legacy-migration"
	NetworkSessionResumeStagePrivacyReconcile = "privacy-reconcile"
	NetworkSessionResumeStageExactRecovery    = "exact-recovery"
	NetworkSessionResumeStageGenericRecovery  = "generic-recovery"
	NetworkSessionResumeStageConnectReplay    = "connect-replay"
	NetworkSessionResumeStageTerminalTeardown = "terminal-teardown"

	NetworkSessionResumeOutcomeNotAttempted = "not-attempted"
	NetworkSessionResumeOutcomeFailed       = "failed"
	NetworkSessionResumeOutcomeIncomplete   = "incomplete"
	NetworkSessionResumeOutcomeSucceeded    = "succeeded"

	NetworkSessionReplayDispositionTerminal    = "terminal"
	NetworkSessionReplayDispositionRetryable   = "retryable"
	NetworkSessionReplayDispositionInterrupted = "interrupted"
	NetworkSessionReplayDispositionIncomplete  = "incomplete"

	NetworkSessionApplySubphaseTUNAddress  = "tun-address"
	NetworkSessionApplySubphaseRoutes      = "routes"
	NetworkSessionApplySubphasePolicyRules = "policy-rules"
	NetworkSessionApplySubphaseDNS         = "dns"
	NetworkSessionApplySubphaseNFTables    = "nftables"

	NetworkSessionCleanupAuthorityNone              = "none"
	NetworkSessionCleanupAuthoritySessionProtection = "session-protection"

	NetworkSessionRecoveryActionRetryResume      = "retry-resume"
	NetworkSessionRecoveryActionContinueTeardown = "continue-teardown"
	NetworkSessionRecoveryActionManualDiagnosis  = "manual-diagnosis"
	NetworkSessionRecoveryActionNone             = "none"
)

var networkSessionFailurePhasePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// NetworkSessionRecoveryState is the bounded, privacy-safe semantic projection
// shared by startup resume diagnostics and recovery inspection/execution. It
// deliberately excludes the saved ConnectRequest, profile/server identity,
// Network Session ID, transaction ID/path, generated config and child output.
// Transaction cleanup authority remains represented separately by ordinary
// RecoveryCandidate values.
type NetworkSessionRecoveryState struct {
	Authority            string `json:"authority"`
	Intent               string `json:"intent"`
	StartupGate          string `json:"startup_gate"`
	ResumeStage          string `json:"resume_stage,omitempty"`
	LastResumeOutcome    string `json:"last_resume_outcome"`
	LastTUNFailurePhase  string `json:"last_tun_failure_phase,omitempty"`
	ReplayDisposition    string `json:"replay_disposition,omitempty"`
	NetworkApplySubphase string `json:"network_apply_subphase,omitempty"`
	RollbackStatus       string `json:"rollback_status,omitempty"`
	TransactionPresent   bool   `json:"transaction_present"`
	LegacyMigration      bool   `json:"legacy_migration"`
	CleanupAuthority     string `json:"cleanup_authority"`
	NextAction           string `json:"next_action"`
}

func ValidateNetworkSessionRecoveryState(state NetworkSessionRecoveryState) error {
	if state.Authority != NetworkSessionRecoveryAuthorityPresent {
		return fmt.Errorf("invalid network session recovery authority %q", state.Authority)
	}
	switch state.Intent {
	case "resume", "disconnect", "terminal":
	default:
		return fmt.Errorf("invalid network session recovery intent %q", state.Intent)
	}
	switch state.StartupGate {
	case NetworkSessionStartupGateBlocked, NetworkSessionStartupGateOpen:
	default:
		return fmt.Errorf("invalid network session startup gate %q", state.StartupGate)
	}
	if state.ResumeStage != "" && !validNetworkSessionResumeStage(state.ResumeStage) {
		return fmt.Errorf("invalid network session resume stage %q", state.ResumeStage)
	}
	switch state.LastResumeOutcome {
	case NetworkSessionResumeOutcomeNotAttempted, NetworkSessionResumeOutcomeFailed, NetworkSessionResumeOutcomeIncomplete, NetworkSessionResumeOutcomeSucceeded:
	default:
		return fmt.Errorf("invalid network session resume outcome %q", state.LastResumeOutcome)
	}
	if state.LastTUNFailurePhase != "" && !networkSessionFailurePhasePattern.MatchString(state.LastTUNFailurePhase) {
		return errors.New("invalid network session TUN failure phase")
	}
	if state.ReplayDisposition != "" {
		switch state.ReplayDisposition {
		case NetworkSessionReplayDispositionTerminal,
			NetworkSessionReplayDispositionRetryable,
			NetworkSessionReplayDispositionInterrupted,
			NetworkSessionReplayDispositionIncomplete:
		default:
			return fmt.Errorf("invalid network session replay disposition %q", state.ReplayDisposition)
		}
		if state.ResumeStage != NetworkSessionResumeStageConnectReplay {
			return errors.New("network session replay disposition requires connect-replay resume stage")
		}
	}
	if state.NetworkApplySubphase != "" {
		switch state.NetworkApplySubphase {
		case NetworkSessionApplySubphaseTUNAddress,
			NetworkSessionApplySubphaseRoutes,
			NetworkSessionApplySubphasePolicyRules,
			NetworkSessionApplySubphaseDNS,
			NetworkSessionApplySubphaseNFTables:
		default:
			return fmt.Errorf("invalid network session apply subphase %q", state.NetworkApplySubphase)
		}
		if state.ResumeStage != NetworkSessionResumeStageConnectReplay || state.LastTUNFailurePhase != "network-apply" {
			return errors.New("network session apply subphase requires connect-replay network-apply failure")
		}
	}
	if state.RollbackStatus != "" {
		switch state.RollbackStatus {
		case "not-started", "completed", "failed", "unknown":
		default:
			return fmt.Errorf("invalid network session rollback status %q", state.RollbackStatus)
		}
	}
	switch state.CleanupAuthority {
	case NetworkSessionCleanupAuthorityNone, NetworkSessionCleanupAuthoritySessionProtection:
	default:
		return fmt.Errorf("invalid network session cleanup authority %q", state.CleanupAuthority)
	}
	switch state.NextAction {
	case NetworkSessionRecoveryActionRetryResume, NetworkSessionRecoveryActionContinueTeardown, NetworkSessionRecoveryActionManualDiagnosis, NetworkSessionRecoveryActionNone:
	default:
		return fmt.Errorf("invalid network session recovery next action %q", state.NextAction)
	}
	if state.LastResumeOutcome == NetworkSessionResumeOutcomeSucceeded {
		if state.StartupGate != NetworkSessionStartupGateOpen {
			return errors.New("successful network session recovery requires an open startup gate")
		}
		if state.NextAction != NetworkSessionRecoveryActionNone {
			return errors.New("successful network session recovery cannot require another action")
		}
		if state.ReplayDisposition != "" || state.NetworkApplySubphase != "" {
			return errors.New("successful network session recovery cannot retain replay failure metadata")
		}
	}
	if state.StartupGate == NetworkSessionStartupGateBlocked && state.LastResumeOutcome == NetworkSessionResumeOutcomeSucceeded {
		return errors.New("blocked startup gate cannot have a successful resume outcome")
	}
	if state.Intent == "resume" && state.NextAction == NetworkSessionRecoveryActionContinueTeardown && state.ReplayDisposition != NetworkSessionReplayDispositionTerminal {
		return errors.New("resume intent can request terminal teardown only for terminal replay disposition")
	}
	if state.Intent != "resume" && state.NextAction == NetworkSessionRecoveryActionRetryResume {
		return errors.New("terminal network session intent cannot request resume")
	}
	if state.Intent == "resume" {
		switch state.ReplayDisposition {
		case NetworkSessionReplayDispositionTerminal:
			if state.NextAction != NetworkSessionRecoveryActionContinueTeardown {
				return errors.New("terminal replay disposition requires continue-teardown")
			}
		case NetworkSessionReplayDispositionIncomplete:
			if state.NextAction != NetworkSessionRecoveryActionManualDiagnosis {
				return errors.New("incomplete replay disposition requires manual-diagnosis")
			}
		case NetworkSessionReplayDispositionRetryable, NetworkSessionReplayDispositionInterrupted:
			if state.NextAction != NetworkSessionRecoveryActionRetryResume {
				return errors.New("retryable or interrupted replay disposition requires retry-resume")
			}
		}
	}
	return nil
}

func CloneNetworkSessionRecoveryState(state *NetworkSessionRecoveryState) *NetworkSessionRecoveryState {
	if state == nil {
		return nil
	}
	cloned := *state
	return &cloned
}

func validNetworkSessionResumeStage(stage string) bool {
	switch strings.TrimSpace(stage) {
	case NetworkSessionResumeStageStateLoad,
		NetworkSessionResumeStageLegacyMigration,
		NetworkSessionResumeStagePrivacyReconcile,
		NetworkSessionResumeStageExactRecovery,
		NetworkSessionResumeStageGenericRecovery,
		NetworkSessionResumeStageConnectReplay,
		NetworkSessionResumeStageTerminalTeardown:
		return true
	default:
		return false
	}
}
