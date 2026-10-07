package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/storejson"
)

const profilesFileName = "profiles.json"

var ErrNotFound = errors.New("profile not found")
var ErrAlreadyExists = errors.New("profile already exists")
var ErrNoSelection = errors.New("no profile selected")
var ErrAmbiguousSelector = errors.New("profile selector is ambiguous")

// Store persists user-owned profiles and the selected profile ID under the
// documented podlaz user state location.
type Store struct {
	path string
}

// SubscriptionUpdateDiff describes how a subscription update changed persisted profiles.
type SubscriptionUpdateDiff struct {
	Imported  int
	Updated   int
	Unchanged int
	Removed   int
}

// NewStore returns a profile store at path. If path is empty, the documented
// XDG user state path is used.
func NewStore(path string) (Store, error) {
	if path == "" {
		defaultPath, err := DefaultStorePath()
		if err != nil {
			return Store{}, err
		}
		path = defaultPath
	}
	return Store{path: path}, nil
}

// DefaultStorePath returns $XDG_STATE_HOME/podlaz/profiles.json or the
// documented ~/.local/state/podlaz/profiles.json fallback.
func DefaultStorePath() (string, error) {
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" || !filepath.IsAbs(stateHome) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve podlaz state directory: %w", err)
		}
		stateHome = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(stateHome, "podlaz", profilesFileName), nil
}

func (s Store) Path() string { return s.path }

func (s Store) List() ([]Profile, error) {
	state, err := s.loadState()
	if err != nil {
		return nil, err
	}
	profiles := append([]Profile(nil), state.Profiles...)
	SortStable(profiles)
	return profiles, nil
}

func (s Store) Get(id string) (Profile, error) {
	state, err := s.loadState()
	if err != nil {
		return Profile{}, err
	}
	if p, ok := profileByID(state.Profiles, id); ok {
		return p, nil
	}
	return Profile{}, fmt.Errorf("%w: %s", ErrNotFound, id)
}

// Resolve accepts an exact stable ID first, otherwise an exact trimmed,
// case-insensitive display name. A display name must resolve uniquely.
func (s Store) Resolve(selector string) (Profile, error) {
	state, err := s.loadState()
	if err != nil {
		return Profile{}, err
	}
	return resolveProfile(state.Profiles, selector)
}

// Select persists the stable ID resolved from selector. Selection is user-owned
// preference only; it does not change the current lifecycle or boot policy.
func (s Store) Select(selector string) (Profile, error) {
	state, err := s.loadState()
	if err != nil {
		return Profile{}, err
	}
	p, err := resolveProfile(state.Profiles, selector)
	if err != nil {
		return Profile{}, err
	}
	if state.SelectedProfileID == p.ID {
		return p, nil
	}
	state.SelectedProfileID = p.ID
	if err := s.saveState(state); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// SelectedID returns the currently valid selected stable ID without mutating
// user state. Stale selection is treated as unselected here so read-only
// surfaces such as list/completion remain read-only.
func (s Store) SelectedID() (string, error) {
	state, err := s.loadState()
	if err != nil {
		return "", err
	}
	if state.SelectedProfileID == "" {
		return "", nil
	}
	if _, ok := profileByID(state.Profiles, state.SelectedProfileID); !ok {
		return "", nil
	}
	return state.SelectedProfileID, nil
}

// ResolveSelected applies the deterministic selected-profile rules used by
// normal user intent. It clears stale state by stable ID only and, when exactly
// one profile remains, persists that profile as the selection before returning
// it. It never retargets by display-name resemblance.
func (s Store) ResolveSelected() (Profile, error) {
	state, err := s.loadState()
	if err != nil {
		return Profile{}, err
	}
	changed := clearStaleSelection(&state)
	if state.SelectedProfileID != "" {
		if changed {
			if err := s.saveState(state); err != nil {
				return Profile{}, err
			}
		}
		p, _ := profileByID(state.Profiles, state.SelectedProfileID)
		return p, nil
	}
	if len(state.Profiles) == 1 {
		state.SelectedProfileID = state.Profiles[0].ID
		if err := s.saveState(state); err != nil {
			return Profile{}, err
		}
		return state.Profiles[0], nil
	}
	if changed {
		if err := s.saveState(state); err != nil {
			return Profile{}, err
		}
	}
	if len(state.Profiles) == 0 {
		return Profile{}, fmt.Errorf("%w: import a profile with `podlaz import <uri|url|file>`", ErrNoSelection)
	}
	return Profile{}, fmt.Errorf("%w: multiple profiles exist; run `podlaz profile use <profile>`", ErrNoSelection)
}

// SelectIfUnset selects id only when no valid selection exists. It is used by
// import workflows that produced exactly one logical profile. A stale selected
// ID is cleared instead of being name-retargeted.
func (s Store) SelectIfUnset(id string) (bool, error) {
	state, err := s.loadState()
	if err != nil {
		return false, err
	}
	changed := clearStaleSelection(&state)
	if state.SelectedProfileID != "" {
		if changed {
			if err := s.saveState(state); err != nil {
				return false, err
			}
		}
		return false, nil
	}
	if _, ok := profileByID(state.Profiles, id); !ok {
		return false, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	state.SelectedProfileID = id
	if err := s.saveState(state); err != nil {
		return false, err
	}
	return true, nil
}

// AddAndSelectIfUnset atomically appends one imported profile and, when no
// valid selection exists, selects that profile in the same profile-store write.
// An existing valid selection is preserved.
func (s Store) AddAndSelectIfUnset(p Profile) (bool, error) {
	if err := Validate(p); err != nil {
		return false, err
	}
	state, err := s.loadState()
	if err != nil {
		return false, err
	}
	clearStaleSelection(&state)
	for _, existing := range state.Profiles {
		if existing.ID == p.ID {
			return false, fmt.Errorf("%w: %s", ErrAlreadyExists, p.ID)
		}
	}
	state.Profiles = append(state.Profiles, p)
	SortStable(state.Profiles)
	selected := false
	if state.SelectedProfileID == "" {
		state.SelectedProfileID = p.ID
		selected = true
	}
	if err := s.saveState(state); err != nil {
		return false, err
	}
	return selected, nil
}

func (s Store) Add(p Profile) error {
	if err := Validate(p); err != nil {
		return err
	}
	state, err := s.loadState()
	if err != nil {
		return err
	}
	clearStaleSelection(&state)
	for _, existing := range state.Profiles {
		if existing.ID == p.ID {
			return fmt.Errorf("%w: %s", ErrAlreadyExists, p.ID)
		}
	}
	state.Profiles = append(state.Profiles, p)
	SortStable(state.Profiles)
	return s.saveState(state)
}

// AddProfiles atomically appends multiple imported profiles. The profile store is
// left untouched when validation or duplicate detection fails before the atomic
// file replacement.
func (s Store) AddProfiles(next []Profile) error {
	state, err := s.loadState()
	if err != nil {
		return err
	}
	clearStaleSelection(&state)
	existingByID := make(map[string]struct{}, len(state.Profiles))
	for _, p := range state.Profiles {
		existingByID[p.ID] = struct{}{}
	}

	seenNext := make(map[string]struct{}, len(next))
	for _, p := range next {
		if err := Validate(p); err != nil {
			return err
		}
		if _, duplicate := seenNext[p.ID]; duplicate {
			return fmt.Errorf("duplicate profile id %q in import batch", p.ID)
		}
		if _, exists := existingByID[p.ID]; exists {
			return fmt.Errorf("%w: %s", ErrAlreadyExists, p.ID)
		}
		seenNext[p.ID] = struct{}{}
	}

	state.Profiles = append(state.Profiles, next...)
	SortStable(state.Profiles)
	return s.saveState(state)
}

// Delete removes an exact stable profile ID and atomically clears selection when
// that ID was selected.
func (s Store) Delete(id string) error {
	state, err := s.loadState()
	if err != nil {
		return err
	}
	kept := state.Profiles[:0]
	deleted := false
	for _, p := range state.Profiles {
		if p.ID == id {
			deleted = true
			continue
		}
		kept = append(kept, p)
	}
	if !deleted {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	state.Profiles = kept
	if state.SelectedProfileID == id {
		state.SelectedProfileID = ""
	}
	SortStable(state.Profiles)
	return s.saveState(state)
}

// ReplaceSubscriptionProfiles atomically replaces the profiles previously owned
// by a subscription with the latest successfully parsed subscription profiles.
// Profiles not owned by the subscription are preserved. Removing the selected
// stable ID clears selection in the same profile-store replacement.
func (s Store) ReplaceSubscriptionProfiles(previousIDs []string, next []Profile) (SubscriptionUpdateDiff, error) {
	state, err := s.loadState()
	if err != nil {
		return SubscriptionUpdateDiff{}, err
	}
	current := state.Profiles

	previous := make(map[string]struct{}, len(previousIDs))
	for _, id := range previousIDs {
		previous[id] = struct{}{}
	}

	existingByID := make(map[string]Profile, len(current))
	for _, p := range current {
		existingByID[p.ID] = p
	}

	seenNext := make(map[string]struct{}, len(next))
	for _, p := range next {
		if err := Validate(p); err != nil {
			return SubscriptionUpdateDiff{}, err
		}
		if p.Source != SourceSubscription {
			return SubscriptionUpdateDiff{}, ValidationError{Messages: []string{fmt.Sprintf("subscription profile %q must have source subscription", p.ID)}}
		}
		if _, ok := seenNext[p.ID]; ok {
			return SubscriptionUpdateDiff{}, fmt.Errorf("duplicate subscription profile id %q", p.ID)
		}
		seenNext[p.ID] = struct{}{}
		if _, ok := existingByID[p.ID]; ok {
			if _, ownedByThisSubscription := previous[p.ID]; !ownedByThisSubscription {
				return SubscriptionUpdateDiff{}, fmt.Errorf("profile id collision with existing profile %q", p.ID)
			}
		}
	}

	diff := SubscriptionUpdateDiff{}
	kept := make([]Profile, 0, len(current)+len(next))
	for _, p := range current {
		if _, remove := previous[p.ID]; remove {
			if _, stillPresent := seenNext[p.ID]; !stillPresent {
				diff.Removed++
			}
			continue
		}
		kept = append(kept, p)
	}

	for _, p := range next {
		if existing, ok := existingByID[p.ID]; !ok {
			diff.Imported++
		} else if reflect.DeepEqual(existing, p) {
			diff.Unchanged++
		} else {
			diff.Updated++
		}
		kept = append(kept, p)
	}

	state.Profiles = kept
	if state.SelectedProfileID != "" {
		if _, ok := profileByID(state.Profiles, state.SelectedProfileID); !ok {
			state.SelectedProfileID = ""
		}
	}
	SortStable(state.Profiles)
	return diff, s.saveState(state)
}

type storeFile struct {
	SchemaVersion     string    `json:"schema_version"`
	Profiles          []Profile `json:"profiles"`
	SelectedProfileID string    `json:"selected_profile_id,omitempty"`
}

func (s Store) load() ([]Profile, error) {
	state, err := s.loadState()
	if err != nil {
		return nil, err
	}
	return state.Profiles, nil
}

func (s Store) loadState() (storeFile, error) {
	file, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return storeFile{SchemaVersion: "v1"}, nil
	}
	if err != nil {
		return storeFile{}, fmt.Errorf("read profile store %s: %w", s.path, err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var data storeFile
	if err := decoder.Decode(&data); err != nil {
		return storeFile{}, fmt.Errorf("read profile store %s: invalid JSON: %w", s.path, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return storeFile{}, fmt.Errorf("read profile store %s: invalid JSON: trailing data", s.path)
	}
	if data.SchemaVersion != "v1" {
		return storeFile{}, fmt.Errorf("read profile store %s: unsupported schema_version %q", s.path, data.SchemaVersion)
	}
	seen := make(map[string]struct{}, len(data.Profiles))
	for _, p := range data.Profiles {
		if err := Validate(p); err != nil {
			return storeFile{}, fmt.Errorf("read profile store %s: stored profile %q is invalid: %w", s.path, p.ID, err)
		}
		if _, ok := seen[p.ID]; ok {
			return storeFile{}, fmt.Errorf("read profile store %s: duplicate profile id %q", s.path, p.ID)
		}
		seen[p.ID] = struct{}{}
	}
	return data, nil
}

func (s Store) save(profiles []Profile) error {
	state, err := s.loadState()
	if err != nil {
		return err
	}
	state.Profiles = profiles
	if clearStaleSelection(&state) {
		// The same atomic write below persists the cleared selection.
	}
	return s.saveState(state)
}

func (s Store) saveWithDirectorySync(profiles []Profile, syncParentDir func(string) error) error {
	state, err := s.loadState()
	if err != nil {
		return err
	}
	state.Profiles = profiles
	clearStaleSelection(&state)
	return s.saveStateWithDirectorySync(state, syncParentDir)
}

func (s Store) saveState(state storeFile) error {
	return s.saveStateWithDirectorySync(state, storejson.SyncDir)
}

func (s Store) saveStateWithDirectorySync(state storeFile, syncParentDir func(string) error) error {
	state.SchemaVersion = "v1"
	err := storejson.WriteFile(s.path, state, storejson.Options{
		TempPattern:   ".profiles-*.tmp",
		DirectoryMode: storejson.DefaultDirectoryMode,
		FileMode:      storejson.DefaultFileMode,
		SyncParentDir: syncParentDir,
	})
	if err != nil {
		return profileStoreWriteError(err)
	}
	return nil
}

func resolveProfile(profiles []Profile, selector string) (Profile, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return Profile{}, fmt.Errorf("%w: empty selector", ErrNotFound)
	}
	if p, ok := profileByID(profiles, selector); ok {
		return p, nil
	}
	key := displayNameKey(selector)
	var matches []Profile
	for _, p := range profiles {
		if displayNameKey(p.Name) == key {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 0:
		return Profile{}, fmt.Errorf("%w: %s", ErrNotFound, selector)
	case 1:
		return matches[0], nil
	default:
		return Profile{}, fmt.Errorf("%w: %q matches %d profiles; use a stable ID from `podlaz profile show`", ErrAmbiguousSelector, selector, len(matches))
	}
}

func profileByID(profiles []Profile, id string) (Profile, bool) {
	for _, p := range profiles {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}

func clearStaleSelection(state *storeFile) bool {
	if state.SelectedProfileID == "" {
		return false
	}
	if _, ok := profileByID(state.Profiles, state.SelectedProfileID); ok {
		return false
	}
	state.SelectedProfileID = ""
	return true
}

func profileStoreWriteError(err error) error {
	var writeErr *storejson.WriteError
	if !errors.As(err, &writeErr) {
		return fmt.Errorf("write profile store: %w", err)
	}
	switch writeErr.Operation {
	case storejson.OperationEncodeJSON, storejson.OperationWriteTempFile:
		return fmt.Errorf("write temporary profile store: %w", writeErr.Err)
	case storejson.OperationCreateDirectory:
		return fmt.Errorf("create profile store directory: %w", writeErr.Err)
	case storejson.OperationCreateTempFile:
		return fmt.Errorf("create temporary profile store: %w", writeErr.Err)
	case storejson.OperationSetTempPermissions:
		return fmt.Errorf("secure temporary profile store: %w", writeErr.Err)
	case storejson.OperationSyncTempFile:
		return fmt.Errorf("sync temporary profile store: %w", writeErr.Err)
	case storejson.OperationCloseTempFile:
		return fmt.Errorf("close temporary profile store: %w", writeErr.Err)
	case storejson.OperationRenameTempFile:
		return fmt.Errorf("replace profile store atomically: %w", writeErr.Err)
	case storejson.OperationSyncParentDirectory:
		return fmt.Errorf("sync profile store parent directory: %w", writeErr.Err)
	default:
		return fmt.Errorf("write profile store: %w", writeErr.Err)
	}
}
