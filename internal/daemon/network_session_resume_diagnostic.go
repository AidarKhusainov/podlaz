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

type networkSessionResumeDiagnostic struct {
	SchemaVersion       string `json:"schema_version"`
	Owner               string `json:"owner"`
	BootID              string `json:"boot_id"`
	ResumeStage         string `json:"resume_stage"`
	LastResumeOutcome   string `json:"last_resume_outcome"`
	TUNFailurePhase     string `json:"tun_failure_phase,omitempty"`
	RollbackStatus      string `json:"rollback_status,omitempty"`
	TransactionPresent  bool   `json:"transaction_present"`
	LegacyMigration     bool   `json:"legacy_migration"`
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
	state := api.NetworkSessionRecoveryState{
		Authority:           api.NetworkSessionRecoveryAuthorityPresent,
		Intent:              "resume",
		StartupGate:         api.NetworkSessionStartupGateBlocked,
		ResumeStage:         record.ResumeStage,
		LastResumeOutcome:   record.LastResumeOutcome,
		LastTUNFailurePhase: record.TUNFailurePhase,
		RollbackStatus:      record.RollbackStatus,
		TransactionPresent:  record.TransactionPresent,
		LegacyMigration:     record.LegacyMigration,
		CleanupAuthority:    api.NetworkSessionCleanupAuthorityNone,
		NextAction:          api.NetworkSessionRecoveryActionRetryResume,
	}
	return api.ValidateNetworkSessionRecoveryState(state)
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

func persistNetworkSessionResumeFailure(continuation networkSessionContinuationStore, err error) error {
	record, ok := networkSessionResumeFailure(err)
	if !ok {
		return err
	}
	store := newNetworkSessionResumeDiagnosticStore(continuation.runtimeDir, continuation.readBootID)
	if persistErr := store.Save(record); persistErr != nil {
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
