package sub

import "fmt"

func (s Store) Delete(id string) error {
	sources, err := s.load()
	if err != nil {
		return err
	}

	kept := sources[:0]
	found := false
	for _, source := range sources {
		if source.ID == id {
			found = true
			continue
		}
		kept = append(kept, source)
	}
	if !found {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}

	sortSources(kept)
	return s.save(kept)
}
