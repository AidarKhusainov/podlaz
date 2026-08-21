package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

const (
	bootAutostartManifestSchemaVersion = "podlaz.boot-autostart-manifest.v1"
	bootAutostartAttemptSchemaVersion  = "podlaz.boot-autostart-attempt.v1"
	bootAutostartManifestFileName      = "boot-autostart-manifest.json"
	bootAutostartAttemptFileName       = "boot-autostart-attempt.json"
	maxBootAutostartStateBytes         = 64 * 1024
)

var errBootAutostartAttemptAlreadyAdmitted = errors.New("boot autostart attempt is already admitted for this boot")

type bootAutostartAttemptState string

const (
	bootAutostartAttemptInProgress bootAutostartAttemptState = "in_progress"
	bootAutostartAttemptSucceeded  bootAutostartAttemptState = "succeeded"
	bootAutostartAttemptTerminal   bootAutostartAttemptState = "terminal"
)

type bootAutostartTerminalReason string

const (
	bootAutostartTerminalConnectFailed  bootAutostartTerminalReason = "connect_failed"
	bootAutostartTerminalSessionFailure bootAutostartTerminalReason = "session_terminal"
)

type bootAutostartManifest struct {
	SchemaVersion    string                        `json:"schema_version"`
	Generation       string                        `json:"generation"`
	ConfiguredBootID string                        `json:"configured_boot_id"`
	Configuration    api.AutostartConfigureRequest `json:"configuration"`
}

func (m bootAutostartManifest) EligibleForBoot(currentBootID string) bool {
	currentBootID = strings.TrimSpace(currentBootID)
	return currentBootID != "" && m.ConfiguredBootID != currentBootID
}

type bootAutostartAttempt struct {
	SchemaVersion      string                        `json:"schema_version"`
	BootID             string                        `json:"boot_id"`
	ManifestGeneration string                        `json:"manifest_generation"`
	State              bootAutostartAttemptState     `json:"state"`
	Configuration      api.AutostartConfigureRequest `json:"configuration"`
	TerminalReason     bootAutostartTerminalReason   `json:"terminal_reason,omitempty"`
}

type bootAutostartManifestStore struct {
	stateDir   string
	readBootID bootIDReader
}

type bootAutostartAttemptStore struct {
	runtimeDir string
	readBootID bootIDReader
}

var bootAutostartAttemptLocks sync.Map

func newBootAutostartManifestStore(stateDir string, readBootID bootIDReader) bootAutostartManifestStore {
	if readBootID == nil {
		readBootID = readLinuxBootID
	}
	return bootAutostartManifestStore{stateDir: stateDir, readBootID: readBootID}
}

func newBootAutostartAttemptStore(runtimeDir string, readBootID bootIDReader) bootAutostartAttemptStore {
	if readBootID == nil {
		readBootID = readLinuxBootID
	}
	return bootAutostartAttemptStore{runtimeDir: runtimeDir, readBootID: readBootID}
}

func (s bootAutostartManifestStore) path() string {
	return filepath.Join(s.stateDir, bootAutostartManifestFileName)
}

func (s bootAutostartAttemptStore) path() string {
	return filepath.Join(s.runtimeDir, bootAutostartAttemptFileName)
}

func (s bootAutostartAttemptStore) mutationLock() *sync.Mutex {
	value, _ := bootAutostartAttemptLocks.LoadOrStore(s.path(), &sync.Mutex{})
	return value.(*sync.Mutex)
}

func (s bootAutostartManifestStore) Enable(configuration api.AutostartConfigureRequest) (bootAutostartManifest, error) {
	if err := api.ValidateAutostartConfigureRequest(configuration); err != nil {
		return bootAutostartManifest{}, fmt.Errorf("validate boot autostart configuration: %w", err)
	}
	bootID, err := requiredBootID(s.readBootID, "boot autostart manifest")
	if err != nil {
		return bootAutostartManifest{}, err
	}
	generation, err := newBootAutostartGeneration()
	if err != nil {
		return bootAutostartManifest{}, err
	}
	manifest := bootAutostartManifest{
		SchemaVersion:    bootAutostartManifestSchemaVersion,
		Generation:       generation,
		ConfiguredBootID: bootID,
		Configuration:    configuration,
	}
	if err := validateBootAutostartManifest(manifest); err != nil {
		return bootAutostartManifest{}, err
	}
	if err := os.MkdirAll(s.stateDir, 0o700); err != nil {
		return bootAutostartManifest{}, fmt.Errorf("create boot autostart state directory: %w", err)
	}
	if err := writeBootAutostartState(s.path(), manifest); err != nil {
		return bootAutostartManifest{}, fmt.Errorf("persist boot autostart manifest: %w", err)
	}
	return manifest, nil
}

func (s bootAutostartManifestStore) Disable() error {
	if strings.TrimSpace(s.stateDir) == "" {
		return errors.New("boot autostart state directory is empty")
	}
	err := os.Remove(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("remove boot autostart manifest: %w", err)
	}
	if err := syncFilesystemDirectory(s.stateDir); err != nil {
		return fmt.Errorf("sync boot autostart state directory: %w", err)
	}
	return nil
}

func (s bootAutostartManifestStore) Load() (bootAutostartManifest, bool, error) {
	var manifest bootAutostartManifest
	exists, err := readBootAutostartState(s.path(), &manifest)
	if err != nil || !exists {
		return bootAutostartManifest{}, exists, err
	}
	if err := validateBootAutostartManifest(manifest); err != nil {
		return bootAutostartManifest{}, false, err
	}
	return manifest, true, nil
}

func (s bootAutostartAttemptStore) Admit(manifest bootAutostartManifest) (bootAutostartAttempt, error) {
	if err := validateBootAutostartManifest(manifest); err != nil {
		return bootAutostartAttempt{}, fmt.Errorf("validate manifest before boot autostart admission: %w", err)
	}
	lock := s.mutationLock()
	lock.Lock()
	defer lock.Unlock()

	bootID, err := requiredBootID(s.readBootID, "boot autostart attempt")
	if err != nil {
		return bootAutostartAttempt{}, err
	}
	if !manifest.EligibleForBoot(bootID) {
		return bootAutostartAttempt{}, errors.New("boot autostart manifest is not eligible in the boot where it was configured")
	}
	if current, exists, err := s.loadCurrentLocked(bootID); err != nil {
		return bootAutostartAttempt{}, err
	} else if exists {
		return current, errBootAutostartAttemptAlreadyAdmitted
	}

	attempt := bootAutostartAttempt{
		SchemaVersion:      bootAutostartAttemptSchemaVersion,
		BootID:             bootID,
		ManifestGeneration: manifest.Generation,
		State:              bootAutostartAttemptInProgress,
		Configuration:      manifest.Configuration,
	}
	if err := validateBootAutostartAttempt(attempt); err != nil {
		return bootAutostartAttempt{}, err
	}
	if err := os.MkdirAll(s.runtimeDir, 0o755); err != nil {
		return bootAutostartAttempt{}, fmt.Errorf("create boot autostart runtime directory: %w", err)
	}
	if err := writeBootAutostartState(s.path(), attempt); err != nil {
		return bootAutostartAttempt{}, fmt.Errorf("persist boot autostart attempt: %w", err)
	}
	return attempt, nil
}

func (s bootAutostartAttemptStore) LoadCurrent() (bootAutostartAttempt, bool, error) {
	lock := s.mutationLock()
	lock.Lock()
	defer lock.Unlock()

	bootID, err := requiredBootID(s.readBootID, "boot autostart attempt")
	if err != nil {
		return bootAutostartAttempt{}, false, err
	}
	return s.loadCurrentLocked(bootID)
}

func (s bootAutostartAttemptStore) loadCurrentLocked(bootID string) (bootAutostartAttempt, bool, error) {
	var attempt bootAutostartAttempt
	exists, err := readBootAutostartState(s.path(), &attempt)
	if err != nil || !exists {
		return bootAutostartAttempt{}, exists, err
	}
	if err := validateBootAutostartAttempt(attempt); err != nil {
		return bootAutostartAttempt{}, false, err
	}
	if attempt.BootID == bootID {
		return attempt, true, nil
	}
	if err := os.Remove(s.path()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return bootAutostartAttempt{}, false, fmt.Errorf("discard previous-boot autostart attempt: %w", err)
	}
	if err := syncFilesystemDirectory(s.runtimeDir); err != nil {
		return bootAutostartAttempt{}, false, fmt.Errorf("sync runtime directory after previous-boot autostart attempt removal: %w", err)
	}
	return bootAutostartAttempt{}, false, nil
}

func (s bootAutostartAttemptStore) MarkSucceeded() error {
	return s.complete(bootAutostartAttemptSucceeded, "")
}

func (s bootAutostartAttemptStore) MarkTerminal(reason bootAutostartTerminalReason) error {
	if !validBootAutostartTerminalReason(reason) {
		return fmt.Errorf("invalid boot autostart terminal reason %q", reason)
	}
	return s.complete(bootAutostartAttemptTerminal, reason)
}

func (s bootAutostartAttemptStore) complete(target bootAutostartAttemptState, reason bootAutostartTerminalReason) error {
	lock := s.mutationLock()
	lock.Lock()
	defer lock.Unlock()

	bootID, err := requiredBootID(s.readBootID, "boot autostart attempt")
	if err != nil {
		return err
	}
	attempt, exists, err := s.loadCurrentLocked(bootID)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("boot autostart attempt does not exist for current boot")
	}
	if attempt.State == target {
		if target != bootAutostartAttemptTerminal || attempt.TerminalReason == reason {
			return nil
		}
		return errors.New("boot autostart terminal attempt already has another reason")
	}
	if attempt.State != bootAutostartAttemptInProgress {
		return fmt.Errorf("cannot change completed boot autostart attempt from %q to %q", attempt.State, target)
	}
	attempt.State = target
	attempt.TerminalReason = reason
	if err := validateBootAutostartAttempt(attempt); err != nil {
		return err
	}
	if err := writeBootAutostartState(s.path(), attempt); err != nil {
		return fmt.Errorf("persist completed boot autostart attempt: %w", err)
	}
	return nil
}

func validateBootAutostartManifest(manifest bootAutostartManifest) error {
	if manifest.SchemaVersion != bootAutostartManifestSchemaVersion {
		return fmt.Errorf("unsupported boot autostart manifest schema %q", manifest.SchemaVersion)
	}
	if !validBootAutostartGeneration(manifest.Generation) {
		return errors.New("boot autostart manifest has invalid generation")
	}
	if strings.TrimSpace(manifest.ConfiguredBootID) == "" {
		return errors.New("boot autostart manifest has empty configured_boot_id")
	}
	if err := api.ValidateAutostartConfigureRequest(manifest.Configuration); err != nil {
		return fmt.Errorf("invalid boot autostart configuration: %w", err)
	}
	return nil
}

func validateBootAutostartAttempt(attempt bootAutostartAttempt) error {
	if attempt.SchemaVersion != bootAutostartAttemptSchemaVersion {
		return fmt.Errorf("unsupported boot autostart attempt schema %q", attempt.SchemaVersion)
	}
	if strings.TrimSpace(attempt.BootID) == "" {
		return errors.New("boot autostart attempt has empty boot_id")
	}
	if !validBootAutostartGeneration(attempt.ManifestGeneration) {
		return errors.New("boot autostart attempt has invalid manifest_generation")
	}
	if err := api.ValidateAutostartConfigureRequest(attempt.Configuration); err != nil {
		return fmt.Errorf("invalid boot autostart attempt configuration: %w", err)
	}
	switch attempt.State {
	case bootAutostartAttemptInProgress, bootAutostartAttemptSucceeded:
		if attempt.TerminalReason != "" {
			return errors.New("non-terminal boot autostart attempt has terminal reason")
		}
	case bootAutostartAttemptTerminal:
		if !validBootAutostartTerminalReason(attempt.TerminalReason) {
			return errors.New("terminal boot autostart attempt has invalid reason")
		}
	default:
		return fmt.Errorf("invalid boot autostart attempt state %q", attempt.State)
	}
	return nil
}

func validBootAutostartTerminalReason(reason bootAutostartTerminalReason) bool {
	switch reason {
	case bootAutostartTerminalConnectFailed, bootAutostartTerminalSessionFailure:
		return true
	default:
		return false
	}
}

func validBootAutostartGeneration(value string) bool {
	if len(value) != 32 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 16
}

func newBootAutostartGeneration() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate boot autostart manifest generation: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func requiredBootID(readBootID bootIDReader, purpose string) (string, error) {
	if readBootID == nil {
		return "", fmt.Errorf("%s boot id reader is nil", purpose)
	}
	bootID, err := readBootID()
	if err != nil {
		return "", fmt.Errorf("read boot id for %s: %w", purpose, err)
	}
	bootID = strings.TrimSpace(bootID)
	if bootID == "" {
		return "", fmt.Errorf("read boot id for %s: empty boot id", purpose)
	}
	return bootID, nil
}

func writeBootAutostartState(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maxBootAutostartStateBytes {
		return fmt.Errorf("boot autostart state exceeds %d bytes", maxBootAutostartStateBytes)
	}
	return atomicWritePrivateFile(path, data)
}

func readBootAutostartState(path string, dst any) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect boot autostart state: %w", err)
	}
	if !info.Mode().IsRegular() {
		return false, errors.New("boot autostart state is not a regular file")
	}
	if info.Mode().Perm() != 0o600 {
		return false, fmt.Errorf("boot autostart state permissions are %o, want 600", info.Mode().Perm())
	}
	if info.Size() > maxBootAutostartStateBytes {
		return false, fmt.Errorf("boot autostart state exceeds %d bytes", maxBootAutostartStateBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("open boot autostart state: %w", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(io.LimitReader(file, maxBootAutostartStateBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return false, fmt.Errorf("decode boot autostart state: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return false, errors.New("decode boot autostart state: trailing data")
		}
		return false, fmt.Errorf("decode boot autostart state trailing data: %w", err)
	}
	return true, nil
}
