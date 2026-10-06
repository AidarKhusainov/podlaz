package profile

import "strings"

// DeleteSubscriptionProfiles removes profiles with source subscription whose IDs
// are owned by the subscription being deleted. Missing owned IDs are ignored so
// cleanup can finish after already-absent profile entries, but profiles with the
// same IDs from manual or one-off imports are preserved. Removing the selected
// stable ID clears selection in the same atomic profile-store replacement.
func (s Store) DeleteSubscriptionProfiles(ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	owned := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		owned[id] = struct{}{}
	}

	state, err := s.loadState()
	if err != nil {
		return 0, err
	}

	kept := state.Profiles[:0]
	removed := 0
	for _, p := range state.Profiles {
		if _, shouldDelete := owned[p.ID]; shouldDelete && p.Source == SourceSubscription {
			removed++
			continue
		}
		kept = append(kept, p)
	}
	if removed == 0 {
		return 0, nil
	}

	state.Profiles = kept
	clearStaleSelection(&state)
	SortStable(state.Profiles)
	return removed, s.saveState(state)
}

// CountUnlinkedProfilesMatchingSubscriptionServers reports profiles that share a
// server with profiles owned by the subscription being deleted, but are not owned
// by that subscription and are not referenced by another subscription metadata
// record. It is diagnostic only; deletion must stay ID/source-based.
func (s Store) CountUnlinkedProfilesMatchingSubscriptionServers(ownedIDs, linkedIDs []string) (int, error) {
	if len(ownedIDs) == 0 {
		return 0, nil
	}

	owned := make(map[string]struct{}, len(ownedIDs))
	for _, id := range ownedIDs {
		owned[id] = struct{}{}
	}
	linked := make(map[string]struct{}, len(linkedIDs))
	for _, id := range linkedIDs {
		linked[id] = struct{}{}
	}

	profiles, err := s.load()
	if err != nil {
		return 0, err
	}

	ownedServers := make(map[string]struct{})
	for _, p := range profiles {
		if _, ok := owned[p.ID]; !ok || p.Source != SourceSubscription {
			continue
		}
		if key := subscriptionServerMatchKey(p); key != "" {
			ownedServers[key] = struct{}{}
		}
	}
	if len(ownedServers) == 0 {
		return 0, nil
	}

	count := 0
	for _, p := range profiles {
		if _, ok := owned[p.ID]; ok {
			continue
		}
		if _, ok := linked[p.ID]; ok {
			continue
		}
		if _, ok := ownedServers[subscriptionServerMatchKey(p)]; ok {
			count++
		}
	}
	return count, nil
}

func subscriptionServerMatchKey(p Profile) string {
	return strings.ToLower(strings.TrimSpace(p.Server))
}
