package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

const (
	productTerminalReasonSchemaVersion = "podlaz.product-terminal-reason.v1"
	productTerminalReasonFileName      = "product-terminal-reason.json"
	maxProductTerminalReasonBytes      = 4 * 1024
)

type productTerminalReasonState struct {
	SchemaVersion string             `json:"schema_version"`
	BootID        string             `json:"boot_id"`
	Reason        api.TerminalReason `json:"reason,omitempty"`
	Superseded    bool               `json:"superseded,omitempty"`
}

type productTerminalReasonResolution struct {
	Reason     api.TerminalReason
	Superseded bool
}

type productTerminalReasonStore struct {
	runtimeDir string
	readBootID bootIDReader
}

func newProductTerminalReasonStore(runtimeDir string, readBootID bootIDReader) productTerminalReasonStore {
	if readBootID == nil {
		readBootID = readLinuxBootID
	}
	return productTerminalReasonStore{runtimeDir: runtimeDir, readBootID: readBootID}
}

func (s productTerminalReasonStore) path() string {
	return filepath.Join(s.runtimeDir, productTerminalReasonFileName)
}

func (s productTerminalReasonStore) Set(reason api.TerminalReason) error {
	if reason == "" {
		return errors.New("terminal reason must not be empty")
	}
	if err := api.ValidateTerminalReason(reason); err != nil {
		return err
	}
	return s.write(productTerminalReasonState{Reason: reason})
}

// Supersede records that a newer explicit lifecycle has replaced any earlier
// terminal product outcome from this boot. The marker is distinct from Clear:
// its presence intentionally prevents status from falling back to an older
// terminal BootAutostartAttempt that must remain durable as no-retry authority.
func (s productTerminalReasonStore) Supersede() error {
	return s.write(productTerminalReasonState{Superseded: true})
}

func (s productTerminalReasonStore) write(state productTerminalReasonState) error {
	bootID, err := requiredBootID(s.readBootID, "product terminal reason")
	if err != nil {
		return err
	}
	state.SchemaVersion = productTerminalReasonSchemaVersion
	state.BootID = bootID
	if err := validateProductTerminalReasonState(state); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode product terminal reason: %w", err)
	}
	data = append(data, '\n')
	if len(data) > maxProductTerminalReasonBytes {
		return errors.New("product terminal reason state is too large")
	}
	if err := os.MkdirAll(s.runtimeDir, 0o755); err != nil {
		return fmt.Errorf("create product terminal reason runtime directory: %w", err)
	}
	if err := atomicWritePrivateFile(s.path(), data); err != nil {
		return fmt.Errorf("persist product terminal reason: %w", err)
	}
	return nil
}

func (s productTerminalReasonStore) LoadCurrent() (api.TerminalReason, bool, error) {
	resolution, exists, err := s.ResolveCurrent()
	if err != nil || !exists || resolution.Superseded {
		return "", false, err
	}
	return resolution.Reason, true, nil
}

func (s productTerminalReasonStore) ResolveCurrent() (productTerminalReasonResolution, bool, error) {
	state, exists, err := s.loadCurrentState()
	if err != nil || !exists {
		return productTerminalReasonResolution{}, exists, err
	}
	return productTerminalReasonResolution{Reason: state.Reason, Superseded: state.Superseded}, true, nil
}

func (s productTerminalReasonStore) loadCurrentState() (productTerminalReasonState, bool, error) {
	info, err := os.Lstat(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return productTerminalReasonState{}, false, nil
	}
	if err != nil {
		return productTerminalReasonState{}, false, fmt.Errorf("inspect product terminal reason: %w", err)
	}
	if !info.Mode().IsRegular() {
		return productTerminalReasonState{}, false, errors.New("product terminal reason state is not a regular file")
	}
	if info.Mode().Perm() != 0o600 {
		return productTerminalReasonState{}, false, fmt.Errorf("product terminal reason permissions are %o, want 600", info.Mode().Perm())
	}
	if info.Size() > maxProductTerminalReasonBytes {
		return productTerminalReasonState{}, false, errors.New("product terminal reason state is too large")
	}

	file, err := os.Open(s.path())
	if err != nil {
		return productTerminalReasonState{}, false, fmt.Errorf("open product terminal reason: %w", err)
	}
	defer file.Close()

	var state productTerminalReasonState
	decoder := json.NewDecoder(io.LimitReader(file, maxProductTerminalReasonBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return productTerminalReasonState{}, false, fmt.Errorf("decode product terminal reason: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return productTerminalReasonState{}, false, errors.New("decode product terminal reason: trailing data")
		}
		return productTerminalReasonState{}, false, fmt.Errorf("decode product terminal reason trailing data: %w", err)
	}
	if err := validateProductTerminalReasonState(state); err != nil {
		return productTerminalReasonState{}, false, err
	}
	bootID, err := requiredBootID(s.readBootID, "product terminal reason")
	if err != nil {
		return productTerminalReasonState{}, false, err
	}
	if state.BootID != bootID {
		if err := s.Clear(); err != nil {
			return productTerminalReasonState{}, false, fmt.Errorf("discard previous-boot terminal reason: %w", err)
		}
		return productTerminalReasonState{}, false, nil
	}
	return state, true, nil
}

func validateProductTerminalReasonState(state productTerminalReasonState) error {
	if state.SchemaVersion != productTerminalReasonSchemaVersion {
		return fmt.Errorf("unsupported product terminal reason schema %q", state.SchemaVersion)
	}
	if state.BootID == "" {
		return errors.New("product terminal reason boot_id is empty")
	}
	if state.Superseded {
		if state.Reason != "" {
			return errors.New("superseded product terminal reason must not contain a reason")
		}
		return nil
	}
	if state.Reason == "" {
		return errors.New("product terminal reason is empty")
	}
	return api.ValidateTerminalReason(state.Reason)
}

func (s productTerminalReasonStore) Clear() error {
	err := os.Remove(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("remove product terminal reason: %w", err)
	}
	if err := syncFilesystemDirectory(s.runtimeDir); err != nil {
		return fmt.Errorf("sync product terminal reason runtime directory: %w", err)
	}
	return nil
}
