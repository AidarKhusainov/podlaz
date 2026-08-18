package daemon

import (
	"context"
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
	networkSessionContinuationSchemaVersion = "podlaz.network-session-continuation.v1"
	networkSessionContinuationOwner         = "podlaz"
	networkSessionContinuationFileName      = "network-session-continuation.json"
	maxNetworkSessionContinuationBytes      = 64 * 1024
)

var errNetworkSessionRecoveryIncomplete = errors.New("network session startup recovery is incomplete")

type bootIDReader func() (string, error)

type networkSessionContinuation struct {
	SchemaVersion string             `json:"schema_version"`
	Owner         string             `json:"owner"`
	BootID        string             `json:"boot_id"`
	Request       api.ConnectRequest `json:"request"`
}

type networkSessionContinuationStore struct {
	runtimeDir string
	readBootID bootIDReader

	// Test-only observation hooks. They deliberately carry no persisted data.
	afterSave   func()
	afterRemove func()
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

func (s networkSessionContinuationStore) Save(request api.ConnectRequest) error {
	if err := api.ValidateConnectRequest(request); err != nil {
		return fmt.Errorf("validate network session continuation request: %w", err)
	}
	bootID, err := s.currentBootID()
	if err != nil {
		return err
	}
	record := networkSessionContinuation{
		SchemaVersion: networkSessionContinuationSchemaVersion,
		Owner:         networkSessionContinuationOwner,
		BootID:        bootID,
		Request:       request,
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode network session continuation: %w", err)
	}
	data = append(data, '\n')
	if len(data) > maxNetworkSessionContinuationBytes {
		return fmt.Errorf("network session continuation exceeds %d bytes", maxNetworkSessionContinuationBytes)
	}
	if err := os.MkdirAll(s.runtimeDir, 0o755); err != nil {
		return fmt.Errorf("create network session runtime directory: %w", err)
	}
	if err := atomicWritePrivateFile(s.path(), data); err != nil {
		return fmt.Errorf("persist network session continuation: %w", err)
	}
	if s.afterSave != nil {
		s.afterSave()
	}
	return nil
}

func (s networkSessionContinuationStore) LoadCurrent() (api.ConnectRequest, bool, error) {
	file, err := os.Open(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return api.ConnectRequest{}, false, nil
	}
	if err != nil {
		return api.ConnectRequest{}, false, fmt.Errorf("open network session continuation: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return api.ConnectRequest{}, false, fmt.Errorf("stat network session continuation: %w", err)
	}
	if info.Mode().Perm() != 0o600 {
		return api.ConnectRequest{}, false, s.discardInvalid(fmt.Errorf("network session continuation permissions are %o, want 600", info.Mode().Perm()))
	}
	if info.Size() > maxNetworkSessionContinuationBytes {
		return api.ConnectRequest{}, false, s.discardInvalid(fmt.Errorf("network session continuation exceeds %d bytes", maxNetworkSessionContinuationBytes))
	}

	data, err := io.ReadAll(io.LimitReader(file, maxNetworkSessionContinuationBytes+1))
	if err != nil {
		return api.ConnectRequest{}, false, fmt.Errorf("read network session continuation: %w", err)
	}
	if len(data) > maxNetworkSessionContinuationBytes {
		return api.ConnectRequest{}, false, s.discardInvalid(fmt.Errorf("network session continuation exceeds %d bytes", maxNetworkSessionContinuationBytes))
	}
	var record networkSessionContinuation
	if err := json.Unmarshal(data, &record); err != nil {
		return api.ConnectRequest{}, false, s.discardInvalid(fmt.Errorf("decode network session continuation: %w", err))
	}
	if err := validateNetworkSessionContinuation(record); err != nil {
		return api.ConnectRequest{}, false, s.discardInvalid(err)
	}
	bootID, err := s.currentBootID()
	if err != nil {
		return api.ConnectRequest{}, false, err
	}
	if record.BootID != bootID {
		if err := s.Remove(); err != nil {
			return api.ConnectRequest{}, false, fmt.Errorf("discard previous-boot network session continuation: %w", err)
		}
		return api.ConnectRequest{}, false, nil
	}
	return record.Request, true, nil
}

func (s networkSessionContinuationStore) Remove() error {
	err := os.Remove(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("remove network session continuation: %w", err)
	}
	if err := syncFilesystemDirectory(s.runtimeDir); err != nil {
		return fmt.Errorf("sync network session runtime directory after continuation removal: %w", err)
	}
	if s.afterRemove != nil {
		s.afterRemove()
	}
	return nil
}

func (s networkSessionContinuationStore) discardInvalid(cause error) error {
	removeErr := s.Remove()
	if removeErr != nil {
		return errors.Join(cause, removeErr)
	}
	return cause
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
	lifecycle     lifecycleService
	continuation networkSessionContinuationStore
}

func newNetworkSessionLifecycle(lifecycle lifecycleService, continuation networkSessionContinuationStore) *networkSessionLifecycle {
	return &networkSessionLifecycle{lifecycle: lifecycle, continuation: continuation}
}

func (l *networkSessionLifecycle) Connect(ctx context.Context, request api.ConnectRequest) (api.LifecycleResponse, error) {
	previous, previousExists, err := l.continuation.LoadCurrent()
	if err != nil {
		return api.LifecycleResponse{}, fmt.Errorf("load existing network session continuation before connect: %w", err)
	}
	if err := l.continuation.Save(request); err != nil {
		return api.LifecycleResponse{}, err
	}
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

func (l *networkSessionLifecycle) Disconnect(ctx context.Context) (api.LifecycleResponse, error) {
	if err := l.continuation.Remove(); err != nil {
		return api.LifecycleResponse{}, fmt.Errorf("disarm network session continuation before disconnect: %w", err)
	}
	return l.lifecycle.Disconnect(ctx)
}

func (l *networkSessionLifecycle) DisconnectForRestart(ctx context.Context) (api.LifecycleResponse, error) {
	return l.lifecycle.Disconnect(ctx)
}

type restartDisconnectLifecycle interface {
	DisconnectForRestart(context.Context) (api.LifecycleResponse, error)
}

type networkSessionStatusFunc func(context.Context) api.StatusResponse
type networkSessionRecoveryFunc func(context.Context, api.StatusResponse) api.RecoveryResponse

func resumeNetworkSession(
	ctx context.Context,
	continuation networkSessionContinuationStore,
	lifecycle lifecycleService,
	status networkSessionStatusFunc,
	recover networkSessionRecoveryFunc,
) (bool, error) {
	request, exists, err := continuation.LoadCurrent()
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	if status == nil || recover == nil {
		return false, errors.New("network session resume requires status and recovery functions")
	}
	recovery := recover(ctx, status(ctx))
	if !networkSessionRecoveryConverged(recovery) {
		return false, errNetworkSessionRecoveryIncomplete
	}
	if _, err := lifecycle.Connect(ctx, request); err != nil {
		return false, fmt.Errorf("resume network session: %w", err)
	}
	return true, nil
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
