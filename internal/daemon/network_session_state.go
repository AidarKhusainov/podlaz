package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

const (
	networkSessionStateSchemaVersion = "podlaz.network-session-state.v1"
	networkSessionStateOwner         = "podlaz"
	maxNetworkSessionStateBytes      = maxNetworkSessionContinuationBytes
)

type networkSessionIntent string

const (
	networkSessionIntentResume     networkSessionIntent = "resume"
	networkSessionIntentDisconnect networkSessionIntent = "disconnect"
	networkSessionIntentTerminal   networkSessionIntent = "terminal"
)

type networkSessionProtectionState string

const (
	networkSessionProtectionUnarmed  networkSessionProtectionState = "unarmed"
	networkSessionProtectionArming   networkSessionProtectionState = "arming"
	networkSessionProtectionArmed    networkSessionProtectionState = "armed"
	networkSessionProtectionRemoving networkSessionProtectionState = "removing"
)

type networkSessionProtection struct {
	State                 networkSessionProtectionState `json:"state"`
	CompositionVersion    int                           `json:"composition_version"`
	Family                string                        `json:"family"`
	Table                 string                        `json:"table"`
	TunInterface          string                        `json:"tun_interface"`
	BootstrapIPv4         []string                      `json:"bootstrap_ipv4"`
	PreviousBootstrapIPv4 []string                      `json:"previous_bootstrap_ipv4,omitempty"`
}

type networkSessionReplacement struct {
	PreviousRequest    api.ConnectRequest          `json:"previous_request"`
	PreviousProtection *networkSessionProtection `json:"previous_protection"`
}

type networkSessionState struct {
	SchemaVersion string                     `json:"schema_version"`
	Owner         string                     `json:"owner"`
	BootID        string                     `json:"boot_id"`
	SessionID     string                     `json:"session_id"`
	Intent        networkSessionIntent       `json:"intent"`
	Request       api.ConnectRequest         `json:"request"`
	Protection    *networkSessionProtection  `json:"protection,omitempty"`
	Replacement   *networkSessionReplacement `json:"replacement,omitempty"`
}

type networkSessionStateStore struct {
	runtimeDir string
	readBootID bootIDReader
}

var (
	networkSessionIDPattern      = regexp.MustCompile(`^[0-9a-f]{32}$`)
	privacyEnvelopeTablePattern  = regexp.MustCompile(`^podlaz_pe_[0-9a-f]{12}(?:_[1-9][0-9]{0,2})?$`)
	networkSessionInterfaceRegex = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)
	networkSessionStateLocks     sync.Map
)

func newNetworkSessionStateStore(runtimeDir string, readBootID bootIDReader) networkSessionStateStore {
	if readBootID == nil {
		readBootID = readLinuxBootID
	}
	return networkSessionStateStore{runtimeDir: runtimeDir, readBootID: readBootID}
}

func (s networkSessionStateStore) path() string {
	return newNetworkSessionContinuationStore(s.runtimeDir, s.readBootID).path()
}

func (s networkSessionStateStore) mutationLock() *sync.Mutex {
	value, _ := networkSessionStateLocks.LoadOrStore(s.path(), &sync.Mutex{})
	return value.(*sync.Mutex)
}

func (s networkSessionStateStore) BeginOrResume(request api.ConnectRequest) (networkSessionState, error) {
	if err := api.ValidateConnectRequest(request); err != nil {
		return networkSessionState{}, fmt.Errorf("validate network session request: %w", err)
	}
	lock := s.mutationLock()
	lock.Lock()
	defer lock.Unlock()

	current, exists, err := s.loadLocked()
	if err != nil {
		return networkSessionState{}, err
	}
	if exists {
		if current.Intent != networkSessionIntentResume {
			return networkSessionState{}, fmt.Errorf("network session is converging with intent %q", current.Intent)
		}
		if current.Replacement != nil {
			if reflect.DeepEqual(current.Request, request) {
				return cloneNetworkSessionState(current), nil
			}
			return networkSessionState{}, errors.New("network session replacement is already in progress")
		}
		if current.Protection != nil {
			if api.NormalizeHandoffPolicy(request.Handoff) == api.HandoffReplacePodlaz {
				if current.Request.Mode != "tun" || request.Mode != "tun" {
					return networkSessionState{}, errors.New("protected replace-podlaz requires TUN mode")
				}
				previousProtection := cloneNetworkSessionProtection(*current.Protection)
				current.Replacement = &networkSessionReplacement{
					PreviousRequest:    current.Request,
					PreviousProtection: &previousProtection,
				}
				current.Request = request
				if err := s.save(current); err != nil {
					return networkSessionState{}, err
				}
				return cloneNetworkSessionState(current), nil
			}
			if !networkSessionRequestsEquivalentIgnoringHandoff(current.Request, request) {
				return networkSessionState{}, errors.New("protected Network Session change requires replace-podlaz handoff")
			}
		}
		current.Request = request
		if err := s.save(current); err != nil {
			return networkSessionState{}, err
		}
		return cloneNetworkSessionState(current), nil
	}

	bootID, err := s.currentBootID()
	if err != nil {
		return networkSessionState{}, err
	}
	sessionID, err := newNetworkSessionID()
	if err != nil {
		return networkSessionState{}, err
	}
	state := networkSessionState{
		SchemaVersion: networkSessionStateSchemaVersion,
		Owner:         networkSessionStateOwner,
		BootID:        bootID,
		SessionID:     sessionID,
		Intent:        networkSessionIntentResume,
		Request:       request,
	}
	if err := s.save(state); err != nil {
		return networkSessionState{}, err
	}
	return cloneNetworkSessionState(state), nil
}

func (s networkSessionStateStore) Load() (networkSessionState, bool, error) {
	lock := s.mutationLock()
	lock.Lock()
	defer lock.Unlock()
	return s.loadLocked()
}

func (s networkSessionStateStore) loadLocked() (networkSessionState, bool, error) {
	file, err := os.Open(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return networkSessionState{}, false, nil
	}
	if err != nil {
		return networkSessionState{}, false, fmt.Errorf("open network session state: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return networkSessionState{}, false, fmt.Errorf("stat network session state: %w", err)
	}
	if info.Mode().Perm() != 0o600 {
		return networkSessionState{}, false, fmt.Errorf("network session state permissions are %o, want 600", info.Mode().Perm())
	}
	if info.Size() > maxNetworkSessionStateBytes {
		return networkSessionState{}, false, fmt.Errorf("network session state exceeds %d bytes", maxNetworkSessionStateBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxNetworkSessionStateBytes+1))
	if err != nil {
		return networkSessionState{}, false, fmt.Errorf("read network session state: %w", err)
	}
	if len(data) > maxNetworkSessionStateBytes {
		return networkSessionState{}, false, fmt.Errorf("network session state exceeds %d bytes", maxNetworkSessionStateBytes)
	}

	var header struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return networkSessionState{}, false, fmt.Errorf("decode network session state header: %w", err)
	}
	if header.SchemaVersion == networkSessionContinuationSchemaVersion {
		return s.migrateContinuation(data)
	}
	if header.SchemaVersion != networkSessionStateSchemaVersion {
		return networkSessionState{}, false, fmt.Errorf("unsupported network session state schema %q", header.SchemaVersion)
	}

	var state networkSessionState
	if err := json.Unmarshal(data, &state); err != nil {
		return networkSessionState{}, false, fmt.Errorf("decode network session state: %w", err)
	}
	if err := validateNetworkSessionState(state); err != nil {
		return networkSessionState{}, false, err
	}
	bootID, err := s.currentBootID()
	if err != nil {
		return networkSessionState{}, false, err
	}
	if state.BootID != bootID {
		if err := s.removeFile(); err != nil {
			return networkSessionState{}, false, fmt.Errorf("discard previous-boot network session state: %w", err)
		}
		return networkSessionState{}, false, nil
	}
	return cloneNetworkSessionState(state), true, nil
}

func (s networkSessionStateStore) Update(update func(*networkSessionState) error) (networkSessionState, bool, error) {
	if update == nil {
		return networkSessionState{}, false, errors.New("network session state update is nil")
	}
	lock := s.mutationLock()
	lock.Lock()
	defer lock.Unlock()

	state, exists, err := s.loadLocked()
	if err != nil || !exists {
		return state, exists, err
	}
	state = cloneNetworkSessionState(state)
	if err := update(&state); err != nil {
		return networkSessionState{}, true, err
	}
	if err := s.save(state); err != nil {
		return networkSessionState{}, true, err
	}
	return cloneNetworkSessionState(state), true, nil
}

func (s networkSessionStateStore) SetIntent(intent networkSessionIntent) error {
	if err := validateNetworkSessionIntent(intent); err != nil {
		return err
	}
	_, _, err := s.Update(func(state *networkSessionState) error {
		state.Intent = intent
		return nil
	})
	return err
}

func (s networkSessionStateStore) SetProtection(protection *networkSessionProtection) error {
	if protection != nil {
		if err := validateNetworkSessionProtection(*protection); err != nil {
			return err
		}
	}
	_, exists, err := s.Update(func(state *networkSessionState) error {
		if protection == nil {
			state.Protection = nil
			return nil
		}
		copyProtection := cloneNetworkSessionProtection(*protection)
		state.Protection = &copyProtection
		return nil
	})
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("cannot persist privacy protection without a network session")
	}
	return nil
}

func (s networkSessionStateStore) CommitReplacement() error {
	_, exists, err := s.Update(func(state *networkSessionState) error {
		if state.Replacement == nil {
			return nil
		}
		if state.Protection == nil || state.Protection.State != networkSessionProtectionArmed || len(state.Protection.PreviousBootstrapIPv4) != 0 {
			return errors.New("cannot commit replacement before final privacy protection is verified")
		}
		state.Replacement = nil
		state.Request.Handoff = api.HandoffBlock
		return nil
	})
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("cannot commit replacement without a network session")
	}
	return nil
}

func (s networkSessionStateStore) RestoreReplacement() error {
	_, exists, err := s.Update(func(state *networkSessionState) error {
		if state.Replacement == nil {
			return nil
		}
		state.Request = state.Replacement.PreviousRequest
		if state.Replacement.PreviousProtection == nil {
			state.Protection = nil
		} else {
			previous := cloneNetworkSessionProtection(*state.Replacement.PreviousProtection)
			state.Protection = &previous
		}
		state.Replacement = nil
		return nil
	})
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("cannot restore replacement without a network session")
	}
	return nil
}

func (s networkSessionStateStore) Remove() error {
	lock := s.mutationLock()
	lock.Lock()
	defer lock.Unlock()

	state, exists, err := s.loadLocked()
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if state.Protection != nil {
		return errors.New("refuse to remove network session state while privacy protection authority exists")
	}
	return s.removeFile()
}

func (s networkSessionStateStore) save(state networkSessionState) error {
	if err := validateNetworkSessionState(state); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode network session state: %w", err)
	}
	data = append(data, '\n')
	if len(data) > maxNetworkSessionStateBytes {
		return fmt.Errorf("network session state exceeds %d bytes", maxNetworkSessionStateBytes)
	}
	if err := os.MkdirAll(s.runtimeDir, 0o755); err != nil {
		return fmt.Errorf("create network session runtime directory: %w", err)
	}
	if err := atomicWritePrivateFile(s.path(), data); err != nil {
		return fmt.Errorf("persist network session state: %w", err)
	}
	return nil
}

func (s networkSessionStateStore) migrateContinuation(data []byte) (networkSessionState, bool, error) {
	var continuation networkSessionContinuation
	if err := json.Unmarshal(data, &continuation); err != nil {
		return networkSessionState{}, false, fmt.Errorf("decode legacy network session continuation: %w", err)
	}
	if err := validateNetworkSessionContinuation(continuation); err != nil {
		if removeErr := s.removeFile(); removeErr != nil {
			return networkSessionState{}, false, errors.Join(err, removeErr)
		}
		return networkSessionState{}, false, err
	}
	bootID, err := s.currentBootID()
	if err != nil {
		return networkSessionState{}, false, err
	}
	if continuation.BootID != bootID {
		if err := s.removeFile(); err != nil {
			return networkSessionState{}, false, fmt.Errorf("discard previous-boot network session continuation: %w", err)
		}
		return networkSessionState{}, false, nil
	}
	sessionID, err := newNetworkSessionID()
	if err != nil {
		return networkSessionState{}, false, err
	}
	state := networkSessionState{
		SchemaVersion: networkSessionStateSchemaVersion,
		Owner:         networkSessionStateOwner,
		BootID:        continuation.BootID,
		SessionID:     sessionID,
		Intent:        networkSessionIntentResume,
		Request:       continuation.Request,
	}
	if err := s.save(state); err != nil {
		return networkSessionState{}, false, fmt.Errorf("migrate network session continuation: %w", err)
	}
	return cloneNetworkSessionState(state), true, nil
}

func (s networkSessionStateStore) removeFile() error {
	err := os.Remove(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("remove network session state: %w", err)
	}
	if err := syncFilesystemDirectory(s.runtimeDir); err != nil {
		return fmt.Errorf("sync network session runtime directory after state removal: %w", err)
	}
	return nil
}

func (s networkSessionStateStore) currentBootID() (string, error) {
	bootID, err := s.readBootID()
	if err != nil {
		return "", fmt.Errorf("read boot id for network session state: %w", err)
	}
	bootID = strings.TrimSpace(bootID)
	if bootID == "" {
		return "", errors.New("read boot id for network session state: empty boot id")
	}
	return bootID, nil
}

func newNetworkSessionID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate network session identity: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func validateNetworkSessionState(state networkSessionState) error {
	if state.SchemaVersion != networkSessionStateSchemaVersion {
		return fmt.Errorf("unsupported network session state schema %q", state.SchemaVersion)
	}
	if state.Owner != networkSessionStateOwner {
		return fmt.Errorf("unsupported network session state owner %q", state.Owner)
	}
	if strings.TrimSpace(state.BootID) == "" {
		return errors.New("network session state has empty boot id")
	}
	if !networkSessionIDPattern.MatchString(state.SessionID) {
		return errors.New("network session state has invalid session id")
	}
	if err := validateNetworkSessionIntent(state.Intent); err != nil {
		return err
	}
	if err := api.ValidateConnectRequest(state.Request); err != nil {
		return fmt.Errorf("invalid network session request: %w", err)
	}
	if state.Protection != nil {
		if err := validateNetworkSessionProtection(*state.Protection); err != nil {
			return err
		}
	}
	if state.Replacement != nil {
		if state.Intent != networkSessionIntentResume {
			return errors.New("network session replacement requires resume intent")
		}
		if api.NormalizeHandoffPolicy(state.Request.Handoff) != api.HandoffReplacePodlaz {
			return errors.New("network session replacement requires replace-podlaz request")
		}
		if err := api.ValidateConnectRequest(state.Replacement.PreviousRequest); err != nil {
			return fmt.Errorf("invalid previous replacement request: %w", err)
		}
		if state.Replacement.PreviousProtection == nil {
			return errors.New("network session replacement has no previous protection authority")
		}
		if err := validateNetworkSessionProtection(*state.Replacement.PreviousProtection); err != nil {
			return fmt.Errorf("invalid previous replacement protection: %w", err)
		}
		if state.Protection == nil || !samePrivacyEnvelopeIdentity(*state.Protection, *state.Replacement.PreviousProtection) {
			return errors.New("network session replacement protection identity changed")
		}
	}
	return nil
}

func validateNetworkSessionIntent(intent networkSessionIntent) error {
	switch intent {
	case networkSessionIntentResume, networkSessionIntentDisconnect, networkSessionIntentTerminal:
		return nil
	default:
		return fmt.Errorf("unsupported network session intent %q", intent)
	}
}

func validateNetworkSessionProtection(protection networkSessionProtection) error {
	switch protection.State {
	case networkSessionProtectionUnarmed, networkSessionProtectionArming, networkSessionProtectionArmed, networkSessionProtectionRemoving:
	default:
		return fmt.Errorf("unsupported network session protection state %q", protection.State)
	}
	if protection.CompositionVersion <= 0 {
		return errors.New("network session protection has invalid composition version")
	}
	if protection.Family != "inet" {
		return fmt.Errorf("network session protection has unsupported nftables family %q", protection.Family)
	}
	if !privacyEnvelopeTablePattern.MatchString(protection.Table) {
		return errors.New("network session protection has invalid nftables table identity")
	}
	if err := validateNetworkSessionInterface(protection.TunInterface); err != nil {
		return err
	}
	if err := validateBootstrapIPv4(protection.BootstrapIPv4, "bootstrap"); err != nil {
		return err
	}
	if len(protection.PreviousBootstrapIPv4) != 0 {
		if protection.State != networkSessionProtectionArming {
			return errors.New("previous privacy composition is only valid while arming")
		}
		if err := validateBootstrapIPv4(protection.PreviousBootstrapIPv4, "previous bootstrap"); err != nil {
			return err
		}
	}
	return nil
}

func validateBootstrapIPv4(values []string, label string) error {
	if len(values) == 0 {
		return fmt.Errorf("network session protection has no %s IPv4 endpoint", label)
	}
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		ip := net.ParseIP(strings.TrimSpace(raw))
		if ip == nil || ip.To4() == nil {
			return fmt.Errorf("network session protection has invalid %s IPv4 endpoint", label)
		}
		normalized := ip.To4().String()
		if normalized != raw {
			return fmt.Errorf("network session protection %s IPv4 endpoint is not normalized", label)
		}
		if _, exists := seen[normalized]; exists {
			return fmt.Errorf("network session protection has duplicate %s IPv4 endpoint", label)
		}
		seen[normalized] = struct{}{}
	}
	return nil
}

func validateNetworkSessionInterface(name string) error {
	if name == "" || len(name) > 15 || name == "." || name == ".." || !networkSessionInterfaceRegex.MatchString(name) {
		return errors.New("network session protection has invalid TUN interface identity")
	}
	return nil
}

func networkSessionRequestsEquivalentIgnoringHandoff(a, b api.ConnectRequest) bool {
	a.Handoff = ""
	b.Handoff = ""
	return reflect.DeepEqual(a, b)
}

func samePrivacyEnvelopeIdentity(a, b networkSessionProtection) bool {
	return a.CompositionVersion == b.CompositionVersion &&
		a.Family == b.Family &&
		a.Table == b.Table &&
		a.TunInterface == b.TunInterface
}

func cloneNetworkSessionState(state networkSessionState) networkSessionState {
	clone := state
	if state.Protection != nil {
		protection := cloneNetworkSessionProtection(*state.Protection)
		clone.Protection = &protection
	}
	if state.Replacement != nil {
		replacement := *state.Replacement
		if state.Replacement.PreviousProtection != nil {
			previousProtection := cloneNetworkSessionProtection(*state.Replacement.PreviousProtection)
			replacement.PreviousProtection = &previousProtection
		}
		clone.Replacement = &replacement
	}
	return clone
}

func cloneNetworkSessionProtection(protection networkSessionProtection) networkSessionProtection {
	clone := protection
	clone.BootstrapIPv4 = append([]string(nil), protection.BootstrapIPv4...)
	clone.PreviousBootstrapIPv4 = append([]string(nil), protection.PreviousBootstrapIPv4...)
	return clone
}
