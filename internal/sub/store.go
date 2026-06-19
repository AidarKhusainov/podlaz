package sub

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

func (s Store) Add(source Source) error {
	if err := ValidateSource(source); err != nil {
		return err
	}
	sources, err := s.load()
	if err != nil {
		return err
	}
	for _, existing := range sources {
		if existing.ID == source.ID {
			return fmt.Errorf("%w: %s", ErrAlreadyExists, source.ID)
		}
	}
	sources = append(sources, source)
	sortSources(sources)
	return s.save(sources)
}

func (s Store) List() ([]Source, error) {
	sources, err := s.load()
	if err != nil {
		return nil, err
	}
	sortSources(sources)
	return sources, nil
}

func (s Store) Get(id string) (Source, error) {
	sources, err := s.load()
	if err != nil {
		return Source{}, err
	}
	for _, source := range sources {
		if source.ID == id {
			return source, nil
		}
	}
	return Source{}, fmt.Errorf("%w: %s", ErrNotFound, id)
}

func (s Store) Update(source Source) error {
	if err := ValidateSource(source); err != nil {
		return err
	}
	sources, err := s.load()
	if err != nil {
		return err
	}
	updated := false
	for i := range sources {
		if sources[i].ID == source.ID {
			sources[i] = source
			updated = true
			break
		}
	}
	if !updated {
		return fmt.Errorf("%w: %s", ErrNotFound, source.ID)
	}
	sortSources(sources)
	return s.save(sources)
}

type storeFile struct {
	SchemaVersion string   `json:"schema_version"`
	Sources       []Source `json:"subscriptions"`
}

func (s Store) load() ([]Source, error) {
	file, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read subscription store %s: %w", s.path, err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var data storeFile
	if err := decoder.Decode(&data); err != nil {
		return nil, fmt.Errorf("read subscription store %s: invalid JSON: %w", s.path, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read subscription store %s: invalid JSON: trailing data", s.path)
	}
	if data.SchemaVersion != "v1" {
		return nil, fmt.Errorf("read subscription store %s: unsupported schema_version %q", s.path, data.SchemaVersion)
	}
	seen := make(map[string]struct{}, len(data.Sources))
	for _, source := range data.Sources {
		if err := ValidateSource(source); err != nil {
			return nil, fmt.Errorf("read subscription store %s: stored subscription %q is invalid: %w", s.path, source.ID, err)
		}
		if _, ok := seen[source.ID]; ok {
			return nil, fmt.Errorf("read subscription store %s: duplicate subscription id %q", s.path, source.ID)
		}
		seen[source.ID] = struct{}{}
	}
	return data.Sources, nil
}

func (s Store) save(sources []Source) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create subscription store directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".subscriptions-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary subscription store: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure temporary subscription store: %w", err)
	}

	encoder := json.NewEncoder(tmp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(storeFile{SchemaVersion: "v1", Sources: sources}); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary subscription store: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary subscription store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary subscription store: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace subscription store atomically: %w", err)
	}
	return nil
}

func sortSources(sources []Source) {
	sort.Slice(sources, func(i, j int) bool { return sources[i].ID < sources[j].ID })
}
