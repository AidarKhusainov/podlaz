package profile

// DeleteSubscriptionProfiles removes profiles with source subscription whose IDs
// are owned by the subscription being deleted. Missing owned IDs are ignored so
// cleanup can finish after already-absent profile entries, but profiles with the
// same IDs from manual or one-off imports are preserved.
func (s Store) DeleteSubscriptionProfiles(ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	owned := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		owned[id] = struct{}{}
	}

	profiles, err := s.load()
	if err != nil {
		return 0, err
	}

	kept := profiles[:0]
	removed := 0
	for _, p := range profiles {
		if _, shouldDelete := owned[p.ID]; shouldDelete && p.Source == SourceSubscription {
			removed++
			continue
		}
		kept = append(kept, p)
	}
	if removed == 0 {
		return 0, nil
	}

	SortStable(kept)
	return removed, s.save(kept)
}
