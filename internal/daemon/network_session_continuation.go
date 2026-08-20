package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

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

type networkSessionLifecycle struct {
	lifecycle    lifecycleService
	continuation networkSessionContinuationStore

	continuationMu sync.Mutex
	explicitStop   bool
}

func newNetworkSessionLifecycle(lifecycle lifecycleService, continuation networkSessionContinuationStore) *networkSessionLifecycle {
	return &networkSessionLifecycle{lifecycle: lifecycle, continuation: continuation}
}

func (l *networkSessionLifecycle) Connect(ctx context.Context, request api.ConnectRequest) (api.LifecycleResponse, error) {
	l.continuationMu.Lock()
	if l.explicitStop {
		l.continuationMu.Unlock()
		return api.LifecycleResponse{}, errLifecycleShuttingDown
	}
	previous, previousExists, err := l.continuation.LoadCurrent()
	if err != nil {
		l.continuationMu.Unlock()
		return api.LifecycleResponse{}, fmt.Errorf("load existing network session continuation before connect: %w", err)
	}
	if err := l.continuation.Save(request); err != nil {
		l.continuationMu.Unlock()
		return api.LifecycleResponse{}, err
	}
	l.continuationMu.Unlock()

	response, connectErr := l.lifecycle.Connect(ctx, request)
	if connectErr == nil {
		return response, nil
	}
	if restoreErr := l.restorePreviousContinuation(previous, previousExists); restoreErr != nil {
		return response, errors.Join(connectErr, restoreErr)
	}
	return response, connectErr
}

func (l *networkSessionLifecycle) restorePreviousContinuation(previous api.ConnectRequest, exists bool) error {
	l.continuationMu.Lock()
	defer l.continuationMu.Unlock()
	if l.explicitStop {
		return nil
	}
	if exists {
		if err := l.continuation.Save(previous); err != nil {
			return fmt.Errorf("restore previous network session continuation after failed connect: %w", err)
		}
		return nil
	}
	if err := l.continuation.Remove(); err != nil {
		return fmt.Errorf("disarm failed network session continuation: %w", err)
	}
	return nil
}

// disarmForExplicitStop makes service-stop intent terminal for this daemon
// instance before waiting for admitted lifecycle mutations to drain. Once set,
// a connect admitted before the shutdown fence cannot save or restore reconnect
// intent after the stop has been declared. Exact transaction and Privacy
// Envelope cleanup authority remain durable until final teardown succeeds.
func (l *networkSessionLifecycle) disarmForExplicitStop() error {
	l.continuationMu.Lock()
	defer l.continuationMu.Unlock()
	l.explicitStop = true
	if err := l.continuation.disarm(networkSessionIntentDisconnect); err != nil {
		return fmt.Errorf("disarm network session continuation for explicit stop: %w", err)
	}
	return nil
}

func (l *networkSessionLifecycle) Disconnect(ctx context.Context) (api.LifecycleResponse, error) {
	l.continuationMu.Lock()
	err := l.continuation.disarm(networkSessionIntentDisconnect)
	l.continuationMu.Unlock()
	if err != nil {
		return api.LifecycleResponse{}, fmt.Errorf("disarm network session continuation before disconnect: %w", err)
	}

	response, disconnectErr := l.lifecycle.Disconnect(ctx)
	if disconnectErr != nil {
		return response, disconnectErr
	}

	l.continuationMu.Lock()
	finalizeErr := l.continuation.finalize()
	l.continuationMu.Unlock()
	if finalizeErr != nil {
		return response, fmt.Errorf("finalize network session state after disconnect: %w", finalizeErr)
	}
	return response, nil
}

func (l *networkSessionLifecycle) DisconnectForRestart(ctx context.Context) (api.LifecycleResponse, error) {
	return l.lifecycle.Disconnect(ctx)
}

type restartDisconnectLifecycle interface {
	DisconnectForRestart(context.Context) (api.LifecycleResponse, error)
}

type networkSessionStatusFunc func(context.Context) api.StatusResponse
type networkSessionRecoveryFunc func(context.Context, api.StatusResponse) api.RecoveryResponse
type networkSessionPrivacyReconcileStage func(context.Context, networkSessionStateStore) error
type networkSessionExactRecoveryStage func(context.Context, string) api.RecoveryResponse
type networkSessionTeardownRecoveryStage func(context.Context, networkSessionStateStore) error

func resumeNetworkSession(
	ctx context.Context,
	continuation networkSessionContinuationStore,
	lifecycle lifecycleService,
	status networkSessionStatusFunc,
	recover networkSessionRecoveryFunc,
) (bool, error) {
	stateStore := continuation.stateStore()
	state, exists, err := stateStore.Load()
	if err != nil {
		return false, err
	}
	if !exists {
		migrated, migrateErr := migrateLegacyUpgradeContinuation(continuation.runtimeDir, continuation)
		if migrateErr != nil {
			return false, fmt.Errorf("migrate legacy package upgrade continuation: %w", migrateErr)
		}
		if migrated {
			state, exists, err = stateStore.Load()
			if err != nil {
				return false, err
			}
			if !exists {
				return false, errors.New("legacy package upgrade migration did not persist continuation")
			}
		}
	}

	reconcilePrivacy := continuation.reconcilePrivacy
	if reconcilePrivacy == nil {
		reconcilePrivacy = reconcileProductionNetworkSessionProtection
	}
	recoverExact := continuation.recoverExact
	if recoverExact == nil {
		recoverExact = recoverExactNetworkSessionTransactions
	}
	continueTeardown := continuation.continueTeardown
	if continueTeardown == nil {
		continueTeardown = continuePersistedNetworkSessionTeardown
	}

	if !exists {
		exactRecovery := recoverExact(ctx, continuation.runtimeDir)
		if !networkSessionRecoveryConverged(exactRecovery) {
			return false, errNetworkSessionRecoveryIncomplete
		}
		return false, nil
	}
	if status == nil || recover == nil {
		return false, errors.New("network session resume requires status and recovery functions")
	}

	switch state.Intent {
	case networkSessionIntentResume:
		if err := reconcilePrivacy(ctx, stateStore); err != nil {
			return false, fmt.Errorf("reconcile network session privacy protection: %w", err)
		}
		exactRecovery := recoverExact(ctx, continuation.runtimeDir)
		if !networkSessionRecoveryConverged(exactRecovery) {
			return false, errNetworkSessionRecoveryIncomplete
		}
		recovery := recover(ctx, status(ctx))
		if !networkSessionRecoveryConverged(recovery) {
			return false, errNetworkSessionRecoveryIncomplete
		}
		if _, err := lifecycle.Connect(ctx, state.Request); err != nil {
			return false, fmt.Errorf("resume network session: %w", err)
		}
		return true, nil

	case networkSessionIntentDisconnect, networkSessionIntentTerminal:
		// A persisted teardown decision is terminal for automatic continuation.
		// Keep the envelope in place while every exact/generic data-plane cleanup
		// stage converges, then deliberately remove protection, verify the
		// remaining host network, and clear the durable session authority.
		exactRecovery := recoverExact(ctx, continuation.runtimeDir)
		if !networkSessionRecoveryConverged(exactRecovery) {
			return false, errNetworkSessionRecoveryIncomplete
		}
		recovery := recover(ctx, status(ctx))
		if !networkSessionRecoveryConverged(recovery) {
			return false, errNetworkSessionRecoveryIncomplete
		}
		if err := continueTeardown(ctx, stateStore); err != nil {
			return false, fmt.Errorf("continue persisted network session teardown: %w", err)
		}
		return false, nil

	default:
		return false, fmt.Errorf("unsupported network session intent %q", state.Intent)
	}
}

func networkSessionRecoveryConverged(response api.RecoveryResponse) bool {
	if len(response.Warnings) != 0 {
		return false
	}
	for _, result := range response.Results {
		if result.Status != "recovered" {
			return false
		}
	}
	return true
}
