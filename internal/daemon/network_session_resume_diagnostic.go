package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

const (
	networkSessionResumeDiagnosticSchemaVersion = "podlaz.network-session-resume-diagnostic.v1"
	networkSessionResumeDiagnosticOwner         = "podlaz"
	networkSessionResumeDiagnosticFileName      = "network-session-resume.json"
	maxNetworkSessionResumeDiagnosticBytes      = 16 * 1024
)

type networkSessionReplayDisposition string

const (
	networkSessionReplayDispositionTerminal    networkSessionReplayDisposition = "terminal"
	networkSessionReplayDispositionRetryable   networkSessionReplayDisposition = "retryable"
	networkSessionReplayDispositionInterrupted networkSessionReplayDisposition = "interrupted"
	networkSessionReplayDispositionIncomplete  networkSessionReplayDisposition = "incomplete"
)

type networkSessionCandidateMutation string

const (
	networkSessionCandidateMutationNotOpened  networkSessionCandidateMutation = "not-opened"
	networkSessionCandidateMutationRolledBack networkSessionCandidateMutation = "rolled-back"
	networkSessionCandidateMutationUnresolved networkSessionCandidateMutation = "unresolved"
)

type networkSessionReplayAttempt struct {
	SessionID                string                          `json:"session_id"`
	RecoveryEpoch            uint64                          `json:"recovery_epoch"`
	ReplayDisposition        networkSessionReplayDisposition `json:"replay_disposition"`
	ResumeStage              string                          `json:"resume_stage"`
	TUNFailurePhase          string                          `json:"tun_failure_phase,omitempty"`
	NetworkApplySubphase     string                          `json:"network_apply_subphase,omitempty"`
	NetworkApplyFailureCause string                          `json:"network_apply_failure_cause,omitempty"`
	RollbackStatus           string                          `json:"rollback_status,omitempty"`
	TransactionPresent       bool                            `json:"transaction_present"`
	TransactionID            string                          `json:"transaction_id,omitempty"`
	LegacyMigration          bool                            `json:"legacy_migration"`
	CandidateMutation        networkSessionCandidateMutation `json:"candidate_mutation"`
}

type networkSessionResumeDiagnostic struct {
	SchemaVersion        string                       `json:"schema_version"`
	Owner                string                       `json:"owner"`
	BootID               string                       `json:"boot_id"`
	RecoveryEpoch        uint64                       `json:"recovery_epoch"`
	ResumeStage          string                       `json:"resume_stage"`
	LastResumeOutcome    string                       `json:"last_resume_outcome"`
	TUNFailurePhase      string                       `json:"tun_failure_phase,omitempty"`
	RollbackStatus       string                       `json:"rollback_status,omitempty"`
	TransactionPresent   bool                         `json:"transaction_present"`
	LegacyMigration      bool                         `json:"legacy_migration"`
	ReplayDisposition    string                       `json:"replay_disposition,omitempty"`
	NetworkApplySubphase string                       `json:"network_apply_subphase,omitempty"`
	Originating          *networkSessionReplayAttempt `json:"originating,omitempty"`
	Current              *networkSessionReplayAttempt `json:"current,omitempty"`
}

type networkSessionResumeDiagnosticStore struct {
	runtimeDir string
	readBootID bootIDReader
}

func newNetworkSessionResumeDiagnosticStore(runtimeDir string, readBootID bootIDReader) networkSessionResumeDiagnosticStore {
	if readBootID == nil {
		readBootID = readLinuxBootID
	}
	return networkSessionResumeDiagnosticStore{runtimeDir: runtimeDir, readBootID: readBootID}
}

func (s networkSessionResumeDiagnosticStore) path() string {
	return filepath.Join(s.runtimeDir, "diagnostics", networkSessionResumeDiagnosticFileName)
}

func (s networkSessionResumeDiagnosticStore) Save(record networkSessionResumeDiagnostic) error {
	bootID, err := s.currentBootID()
	if err != nil {
		return err
	}
	record.SchemaVersion = networkSessionResumeDiagnosticSchemaVersion
	record.Owner = networkSessionResumeDiagnosticOwner
	record.BootID = bootID
	if err := validateNetworkSessionResumeDiagnostic(record); err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode network session resume diagnostic: %w", err)
	}
	data = append(data, '\n')
	if len(data) > maxNetworkSessionResumeDiagnosticBytes {
		return fmt.Errorf("network session resume diagnostic exceeds %d bytes", maxNetworkSessionResumeDiagnosticBytes)
	}
	if err := os.MkdirAll(filepath.Dir(s.path()), 0o755); err != nil {
		return fmt.Errorf("create network session diagnostics directory: %w", err)
	}
	if err := atomicWritePrivateFile(s.path(), data); err != nil {
		return fmt.Errorf("persist network session resume diagnostic: %w", err)
	}
	return nil
}

func (s networkSessionResumeDiagnosticStore) SaveLatestBlocker(record networkSessionResumeDiagnostic) error {
	current, exists, err := s.Load()
	if err != nil {
		return err
	}
	if exists {
		record.Originating = cloneNetworkSessionReplayAttempt(current.Originating)
		record.Current = cloneNetworkSessionReplayAttempt(current.Current)
	}
	if record.ResumeStage != api.NetworkSessionResumeStageConnectReplay {
		record.ReplayDisposition = ""
		record.NetworkApplySubphase = ""
	}
	return s.Save(record)
}

func (s networkSessionResumeDiagnosticStore) Load() (networkSessionResumeDiagnostic, bool, error) {
	file, err := os.Open(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return networkSessionResumeDiagnostic{}, false, nil
	}
	if err != nil {
		return networkSessionResumeDiagnostic{}, false, fmt.Errorf("open network session resume diagnostic: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return networkSessionResumeDiagnostic{}, false, fmt.Errorf("stat network session resume diagnostic: %w", err)
	}
	if info.Mode().Perm() != 0o600 {
		return networkSessionResumeDiagnostic{}, false, fmt.Errorf("network session resume diagnostic permissions are %o, want 600", info.Mode().Perm())
	}
	if info.Size() > maxNetworkSessionResumeDiagnosticBytes {
		return networkSessionResumeDiagnostic{}, false, fmt.Errorf("network session resume diagnostic exceeds %d bytes", maxNetworkSessionResumeDiagnosticBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxNetworkSessionResumeDiagnosticBytes+1))
	if err != nil {
		return networkSessionResumeDiagnostic{}, false, fmt.Errorf("read network session resume diagnostic: %w", err)
	}
	if len(data) > maxNetworkSessionResumeDiagnosticBytes {
		return networkSessionResumeDiagnostic{}, false, fmt.Errorf("network session resume diagnostic exceeds %d bytes", maxNetworkSessionResumeDiagnosticBytes)
	}
	var record networkSessionResumeDiagnostic
	if err := json.Unmarshal(data, &record); err != nil {
		return networkSessionResumeDiagnostic{}, false, fmt.Errorf("decode network session resume diagnostic: %w", err)
	}
	if err := validateNetworkSessionResumeDiagnostic(record); err != nil {
		return networkSessionResumeDiagnostic{}, false, err
	}
	bootID, err := s.currentBootID()
	if err != nil {
		return networkSessionResumeDiagnostic{}, false, err
	}
	if record.BootID != bootID {
		return networkSessionResumeDiagnostic{}, false, nil
	}
	return record, true, nil
}

func (s networkSessionResumeDiagnosticStore) Remove() error {
	err := os.Remove(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("remove network session resume diagnostic: %w", err)
	}
	return syncFilesystemDirectory(filepath.Dir(s.path()))
}

func (s networkSessionResumeDiagnosticStore) currentBootID() (string, error) {
	bootID, err := s.readBootID()
	if err != nil {
		return "", fmt.Errorf("read boot id for network session resume diagnostic: %w", err)
	}
	bootID = strings.TrimSpace(bootID)
	if bootID == "" {
		return "", errors.New("read boot id for network session resume diagnostic: empty boot id")
	}
	return bootID, nil
}

func validateNetworkSessionResumeDiagnostic(record networkSessionResumeDiagnostic) error {
	if record.SchemaVersion != networkSessionResumeDiagnosticSchemaVersion {
		return fmt.Errorf("unsupported network session resume diagnostic schema %q", record.SchemaVersion)
	}
	if record.Owner != networkSessionResumeDiagnosticOwner {
		return fmt.Errorf("unsupported network session resume diagnostic owner %q", record.Owner)
	}
	if strings.TrimSpace(record.BootID) == "" {
		return errors.New("network session resume diagnostic has empty boot id")
	}
	if record.ReplayDisposition != "" && !validNetworkSessionReplayDisposition(networkSessionReplayDisposition(record.ReplayDisposition)) {
		return fmt.Errorf("invalid network session replay disposition %q", record.ReplayDisposition)
	}
	if record.NetworkApplySubphase != "" && !validNetworkSessionApplySubphase(record.NetworkApplySubphase) {
		return fmt.Errorf("invalid network session apply subphase %q", record.NetworkApplySubphase)
	}
	if record.Originating != nil {
		if err := validateNetworkSessionReplayAttempt(*record.Originating); err != nil {
			return fmt.Errorf("invalid originating replay attempt: %w", err)
		}
	}
	if record.Current != nil {
		if err := validateNetworkSessionReplayAttempt(*record.Current); err != nil {
			return fmt.Errorf("invalid current replay attempt: %w", err)
		}
	}
	state := api.NetworkSessionRecoveryState{
		Authority:            api.NetworkSessionRecoveryAuthorityPresent,
		Intent:               "resume",
		StartupGate:          api.NetworkSessionStartupGateBlocked,
		ResumeStage:          record.ResumeStage,
		LastResumeOutcome:    record.LastResumeOutcome,
		LastTUNFailurePhase:  record.TUNFailurePhase,
		ReplayDisposition:    record.ReplayDisposition,
		NetworkApplySubphase: record.NetworkApplySubphase,
		RollbackStatus:       record.RollbackStatus,
		TransactionPresent:   record.TransactionPresent,
		LegacyMigration:      record.LegacyMigration,
		CleanupAuthority:     api.NetworkSessionCleanupAuthorityNone,
		NextAction:           networkSessionResumeRecoveryAction(record.ReplayDisposition),
	}
	return api.ValidateNetworkSessionRecoveryState(state)
}

func validateNetworkSessionReplayAttempt(attempt networkSessionReplayAttempt) error {
	if !networkSessionIDPattern.MatchString(strings.TrimSpace(attempt.SessionID)) {
		return errors.New("invalid Network Session identity")
	}
	if attempt.RecoveryEpoch == 0 {
		return errors.New("replay attempt has zero recovery epoch")
	}
	if attempt.ResumeStage != api.NetworkSessionResumeStageConnectReplay {
		return fmt.Errorf("replay attempt has invalid resume stage %q", attempt.ResumeStage)
	}
	if !validNetworkSessionReplayDisposition(attempt.ReplayDisposition) {
		return fmt.Errorf("invalid replay disposition %q", attempt.ReplayDisposition)
	}
	if attempt.NetworkApplySubphase != "" && !validNetworkSessionApplySubphase(attempt.NetworkApplySubphase) {
		return fmt.Errorf("invalid replay apply subphase %q", attempt.NetworkApplySubphase)
	}
	if attempt.NetworkApplyFailureCause != "" {
		if attempt.TUNFailurePhase != "network-apply" {
			return errors.New("network apply failure cause requires network-apply TUN failure phase")
		}
		if !validNetworkSessionApplyFailureCause(attempt.NetworkApplyFailureCause) {
			return fmt.Errorf("invalid network apply failure cause %q", attempt.NetworkApplyFailureCause)
		}
	}
	state := api.NetworkSessionRecoveryState{
		Authority:            api.NetworkSessionRecoveryAuthorityPresent,
		Intent:               "resume",
		StartupGate:          api.NetworkSessionStartupGateBlocked,
		ResumeStage:          attempt.ResumeStage,
		LastResumeOutcome:    api.NetworkSessionResumeOutcomeFailed,
		LastTUNFailurePhase:  attempt.TUNFailurePhase,
		ReplayDisposition:    string(attempt.ReplayDisposition),
		NetworkApplySubphase: attempt.NetworkApplySubphase,
		RollbackStatus:       attempt.RollbackStatus,
		TransactionPresent:   attempt.TransactionPresent,
		LegacyMigration:      attempt.LegacyMigration,
		CleanupAuthority:     api.NetworkSessionCleanupAuthorityNone,
		NextAction:           networkSessionResumeRecoveryAction(string(attempt.ReplayDisposition)),
	}
	if err := api.ValidateNetworkSessionRecoveryState(state); err != nil {
		return err
	}
	switch attempt.CandidateMutation {
	case networkSessionCandidateMutationNotOpened, networkSessionCandidateMutationRolledBack, networkSessionCandidateMutationUnresolved:
	default:
		return fmt.Errorf("invalid candidate mutation outcome %q", attempt.CandidateMutation)
	}
	return nil
}

func validNetworkSessionReplayDisposition(disposition networkSessionReplayDisposition) bool {
	switch disposition {
	case networkSessionReplayDispositionTerminal,
		networkSessionReplayDispositionRetryable,
		networkSessionReplayDispositionInterrupted,
		networkSessionReplayDispositionIncomplete:
		return true
	default:
		return false
	}
}

func validNetworkSessionApplySubphase(subphase string) bool {
	switch strings.TrimSpace(subphase) {
	case "tun-address", "routes", "policy-rules", "dns", "nftables":
		return true
	default:
		return false
	}
}

func validNetworkSessionApplyFailureCause(cause string) bool {
	switch strings.TrimSpace(cause) {
	case "command-exit", "command-timeout", "command-unavailable":
		return true
	default:
		return false
	}
}

func cloneNetworkSessionReplayAttempt(attempt *networkSessionReplayAttempt) *networkSessionReplayAttempt {
	if attempt == nil {
		return nil
	}
	cloned := *attempt
	return &cloned
}

type networkSessionResumeStageError struct {
	stage              string
	outcome            string
	tunFailurePhase    string
	rollbackStatus     string
	transactionPresent bool
	legacyMigration    bool
	err                error
}

func (e networkSessionResumeStageError) Error() string {
	if e.err == nil {
		return "network session resume failed"
	}
	return e.err.Error()
}

func (e networkSessionResumeStageError) Unwrap() error { return e.err }

func newNetworkSessionResumeError(stage string, legacyMigration bool, err error) error {
	return newNetworkSessionResumeOutcomeError(stage, api.NetworkSessionResumeOutcomeFailed, legacyMigration, false, err)
}

func newNetworkSessionResumeOutcomeError(stage, outcome string, legacyMigration, transactionPresent bool, err error) error {
	if err == nil {
		return nil
	}
	phase, transactionID, rollbackStatus := tunFailureLogFields(err)
	if phase == "unknown" {
		phase = ""
	}
	if rollbackStatus == "unknown" && phase == "" {
		rollbackStatus = ""
	}
	if transactionID != "" && transactionID != noTunTransactionID {
		transactionPresent = true
	}
	return networkSessionResumeStageError{
		stage:              stage,
		outcome:            outcome,
		tunFailurePhase:    phase,
		rollbackStatus:     rollbackStatus,
		transactionPresent: transactionPresent,
		legacyMigration:    legacyMigration,
		err:                err,
	}
}

func networkSessionResumeFailure(err error) (networkSessionResumeDiagnostic, bool) {
	var staged networkSessionResumeStageError
	if !errors.As(err, &staged) {
		return networkSessionResumeDiagnostic{}, false
	}
	outcome := staged.outcome
	if outcome == "" {
		outcome = api.NetworkSessionResumeOutcomeFailed
	}
	return networkSessionResumeDiagnostic{
		SchemaVersion:      networkSessionResumeDiagnosticSchemaVersion,
		Owner:              networkSessionResumeDiagnosticOwner,
		ResumeStage:        staged.stage,
		LastResumeOutcome:  outcome,
		TUNFailurePhase:    staged.tunFailurePhase,
		RollbackStatus:     staged.rollbackStatus,
		TransactionPresent: staged.transactionPresent,
		LegacyMigration:    staged.legacyMigration,
	}, true
}

func persistNetworkSessionResumeFailure(continuation networkSessionContinuationStore, recoveryEpoch uint64, err error) error {
	record, ok := networkSessionResumeFailure(err)
	if !ok {
		return err
	}
	record.RecoveryEpoch = recoveryEpoch
	store := newNetworkSessionResumeDiagnosticStore(continuation.runtimeDir, continuation.readBootID)
	if persistErr := store.SaveLatestBlocker(record); persistErr != nil {
		return errors.Join(err, persistErr)
	}
	return err
}

func networkSessionRecoveryResponseHasTransaction(response api.RecoveryResponse) bool {
	for _, result := range response.Results {
		if result.Candidate.Transaction != nil {
			return true
		}
	}
	return false
}
