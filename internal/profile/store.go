package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
)

const profilesFileName = "profiles.json"

var ErrNotFound = errors.New("profile not found")
var ErrAlreadyExists = errors.New("profile already exists")

// Store persists user-owned profiles under the documented TunWarden user state location.
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

// DefaultStorePath returns $XDG_STATE_HOME/tunwarden/profiles.json or the
// documented ~/.local/state/tunwarden/profiles.json fallback.
func DefaultStorePath() (string, error) {
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" || !filepath.IsAbs(stateHome) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve TunWarden state directory: %w", err)
		}
		stateHome = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(stateHome, "tunwarden", profilesFileName), nil
}

func (s Store) Path() string { return s.path }

func (s Store) List() ([]Profile, error) {
	profiles, err := s.load()
	if err != nil {
		return nil, err
	}
	SortStable(profiles)
	return profiles, nil
}

func (s Store) Get(id string) (Profile, error) {
	profiles, err := s.load()
	if err != nil {
		return Profile{}, err
	}
	for _, p := range profiles {
		if p.ID == id {
			return p, nil
		}
	}
	return Profile{}, fmt.Errorf("%w: %s", ErrNotFound, id)
}

func (s Store) Add(p Profile) error {
	if err := Validate(p); err != nil {
		return err
	}
	profiles, err := s.load()
	if err != nil {
		return err
	}
	for _, existing := range profiles {
		if existing.ID == p.ID {
			return fmt.Errorf("%w: %s", ErrAlreadyExists, p.ID)
		}
	}
	profiles = append(profiles, p)
	SortStable(profiles)
	return s.save(profiles)
}

// AddProfiles atomically appends multiple imported profiles. The profile store is
// left untouched when validation or duplicate detection fails before the atomic
// file replacement.
func (s Store) AddProfiles(next []Profile) error {
	current, err := s.load()
	if err != nil {
		return err
	}
	existingByID := make(map[string]struct{}, len(current))
	for _, p := range current {
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

	profiles := make([]Profile, 0, len(current)+len(next))
	profiles = append(profiles, current...)
	profiles = append(profiles, next...)
	SortStable(profiles)
	return s.save(profiles)
}

func (s Store) Delete(id string) error {
	profiles, err := s.load()
	if err != nil {
		return err
	}
	kept := profiles[:0]
	deleted := false
	for _, p := range profiles {
		if p.ID == id {
			deleted = true
			continue
		}
		kept = append(kept, p)
	}
	if !deleted {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	SortStable(kept)
	return s.save(kept)
}

// ReplaceSubscriptionProfiles atomically replaces the profiles previously owned
// by a subscription with the latest successfully parsed subscription profiles.
// Profiles not owned by the subscription are preserved. The existing store file
// is left untouched when validation fails before the atomic file replacement.
func (s Store) ReplaceSubscriptionProfiles(previousIDs []string, next []Profile) (SubscriptionUpdateDiff, error) {
	current, err := s.load()
	if err != nil {
		return SubscriptionUpdateDiff{}, err
	}

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

	SortStable(kept)
	return diff, s.save(kept)
}

type storeFile struct {
	SchemaVersion string    `json:"schema_version"`
	Profiles      []Profile `json:"profiles"`
}

func (s Store) load() ([]Profile, error) {
	file, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read profile store %s: %w", s.path, err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var data storeFile
	if err := decoder.Decode(&data); err != nil {
		return nil, fmt.Errorf("read profile store %s: invalid JSON: %w", s.path, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read profile store %s: invalid JSON: trailing data", s.path)
	}
	if data.SchemaVersion != "v1" {
		return nil, fmt.Errorf("read profile store %s: unsupported schema_version %q", s.path, data.SchemaVersion)
	}
	seen := make(map[string]struct{}, len(data.Profiles))
	for _, p := range data.Profiles {
		if err := Validate(p); err != nil {
			return nil, fmt.Errorf("read profile store %s: stored profile %q is invalid: %w", s.path, p.ID, err)
		}
		if _, ok := seen[p.ID]; ok {
			return nil, fmt.Errorf("read profile store %s: duplicate profile id %q", s.path, p.ID)
		}
		seen[p.ID] = struct{}{}
	}
	return data.Profiles, nil
}

func (s Store) save(profiles []Profile) error {
	return s.saveWithDirectorySync(profiles, syncDir)
}

func (s Store) saveWithDirectorySync(profiles []Profile, syncParentDir func(string) error) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create profile store directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".profiles-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary profile store: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure temporary profile store: %w", err)
	}

	encoder := json.NewEncoder(tmp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(storeFile{SchemaVersion: "v1", Profiles: profiles}); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary profile store: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary profile store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary profile store: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace profile store atomically: %w", err)
	}
	if err := syncParentDir(dir); err != nil {
		return fmt.Errorf("sync profile store parent directory: %w", err)
	}
	return nil
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open parent directory %s for sync: %w", path, err)
	}
	if err := dir.Sync(); err != nil {
		_ = dir.Close()
		return fmt.Errorf("sync parent directory %s: %w", path, err)
	}
	if err := dir.Close(); err != nil {
		return fmt.Errorf("close parent directory %s after sync: %w", path, err)
	}
	return nil
}
