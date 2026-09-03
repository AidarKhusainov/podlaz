package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

const (
	networkSessionContinuationSchemaVersion = "podlaz.network-session-continuation.v1"
	networkSessionContinuationOwner         = "podlaz"
	networkSessionContinuationFileName      = "network-session-continuation.json"
	maxNetworkSessionContinuationBytes      = 64 * 1024
)

var errNetworkSessionRecoveryIncomplete = errors.New("network session startup recovery is incomplete")

// networkSessionContinuation is the legacy #259 reconnect-intent record. New
// writes use networkSessionState, while this shape remains readable so package
// upgrades do not lose an already-authorized same-boot continuation.
type networkSessionContinuation struct {
	SchemaVersion string             `json:"schema_version"`
	Owner         string             `json:"owner"`
	BootID        string             `json:"boot_id"`
	Request       api.ConnectRequest `json:"request"`
}

type bootIDReader func() (string, error)
type networkSessionLegacyMigrationStage func(string, networkSessionContinuationStore) (bool, error)

type networkSessionContinuationStore struct {
	runtimeDir string
	readBootID bootIDReader

	// Test-only observation hooks. afterRemove means reconnect intent has been
	// disarmed; the durable session record may intentionally remain as exact
	// cleanup authority until teardown converges.
	afterSave   func()
	afterRemove func()

	// Test-only startup stage seams. Production leaves them nil so
	// resumeNetworkSession selects the exact production stages below. Keeping
	// seams on the store lets tests execute the same orchestration function the
	// daemon uses instead of maintaining a second, drift-prone startup wrapper.
	migrateLegacy    networkSessionLegacyMigrationStage
	reconcilePrivacy networkSessionPrivacyReconcileStage
	recoverExact     networkSessionExactRecoveryStage
	continueTeardown networkSessionTeardownRecoveryStage
}

func newNetworkSessionContinuationStore(runtimeDir string, readBootID bootIDReader) networkSessionContinuationStore {
	if readBootID == nil {
		readBootID = readLinuxBootID
	}
	return networkSessionContinuationStore{runtimeDir: runtimeDir, readBootID: readBootID}
}

func (s networkSessionContinuationStore) path() string {
	return filepath.Join(s.runtimeDir, networkSessionContinuationFileName)
}

func (s networkSessionContinuationStore) stateStore() networkSessionStateStore {
	return newNetworkSessionStateStore(s.runtimeDir, s.readBootID)
}

func (s networkSessionContinuationStore) Save(request api.ConnectRequest) error {
	if _, err := s.stateStore().BeginOrResume(request); err != nil {
		return fmt.Errorf("persist network session continuation: %w", err)
	}
	if s.afterSave != nil {
		s.afterSave()
	}
	return nil
}

func (s networkSessionContinuationStore) LoadCurrent() (api.ConnectRequest, bool, error) {
	state, exists, err := s.stateStore().Load()
	if err != nil {
		return api.ConnectRequest{}, false, err
	}
	if !exists || state.Intent != networkSessionIntentResume {
		return api.ConnectRequest{}, false, nil
	}
	return state.Request, true, nil
}

// Remove disarms reconnect intent before attempting to finalize the volatile
// Network Session record. If exact Privacy Envelope cleanup authority remains,
// finalization fails but the session stays terminally disarmed and recoverable.
func (s networkSessionContinuationStore) Remove() error {
	stateStore := s.stateStore()
	disarmed := false
	_, exists, err := stateStore.Update(func(state *networkSessionState) error {
		if state.Intent == networkSessionIntentResume {
			state.Intent = networkSessionIntentDisconnect
			disarmed = true
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if disarmed && s.afterRemove != nil {
		s.afterRemove()
	}
	return stateStore.Remove()
}

func (s networkSessionContinuationStore) disarm(intent networkSessionIntent) error {
	if err := validateNetworkSessionIntent(intent); err != nil {
		return err
	}
	stateStore := s.stateStore()
	changed := false
	_, exists, err := stateStore.Update(func(state *networkSessionState) error {
		if state.Intent == intent || state.Intent == networkSessionIntentTerminal {
			return nil
		}
		if state.Intent == networkSessionIntentDisconnect && intent == networkSessionIntentTerminal {
			state.Intent = networkSessionIntentTerminal
			changed = true
			return nil
		}
		if state.Intent != networkSessionIntentResume {
			return fmt.Errorf("cannot change network session intent from %q to %q", state.Intent, intent)
		}
		state.Intent = intent
		changed = true
		return nil
	})
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if changed && s.afterRemove != nil {
		s.afterRemove()
	}
	return nil
}

func (s networkSessionContinuationStore) finalize() error {
	return s.stateStore().Remove()
}

func (s networkSessionContinuationStore) currentBootID() (string, error) {
	bootID, err := s.readBootID()
	if err != nil {
		return "", fmt.Errorf("read boot id for network session continuation: %w", err)
	}
	bootID = strings.TrimSpace(bootID)
	if bootID == "" {
		return "", errors.New("read boot id for network session continuation: empty boot id")
	}
	return bootID, nil
}

func validateNetworkSessionContinuation(record networkSessionContinuation) error {
	if record.SchemaVersion != networkSessionContinuationSchemaVersion {
		return fmt.Errorf("unsupported network session continuation schema %q", record.SchemaVersion)
	}
	if record.Owner != networkSessionContinuationOwner {
		return fmt.Errorf("unsupported network session continuation owner %q", record.Owner)
	}
	if strings.TrimSpace(record.BootID) == "" {
		return errors.New("network session continuation has empty boot id")
	}
	if err := api.ValidateConnectRequest(record.Request); err != nil {
		return fmt.Errorf("invalid network session continuation request: %w", err)
	}
	return nil
}

func readLinuxBootID() (string, error) {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func atomicWritePrivateFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	removeTmp = false
	return syncFilesystemDirectory(dir)
}

func syncFilesystemDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
