package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/sub"
)

func runSubscriptionUpdate(ctx context.Context, store sub.Store, profileStore profile.Store, args []string, stdout io.Writer) error {
	id, err := parseSubscriptionUpdateArgs(args)
	if err != nil {
		return err
	}
	source, err := store.Get(id)
	if err != nil {
		return subscriptionCommandError(err)
	}
	fetchResult, err := sub.FetchSourceWithMetadata(ctx, source)
	if err != nil {
		return err
	}
	content := fetchResult.Content
	format, parsed, err := sub.ParseSubscriptionContent(content)
	if err != nil {
		return err
	}
	providerName, providerNameWarnings := sub.ProviderSubscriptionDisplayNameFromMetadata(format, content, fetchResult.Header)
	parsed.Warnings = append(parsed.Warnings, providerNameWarnings...)
	source = sub.RefreshProviderDisplayName(source, providerName)
	profileSnapshot, profileExisted, err := snapshotFile(profileStore.Path())
	if err != nil {
		return err
	}
	diff, err := profileStore.ReplaceSubscriptionProfiles(source.ProfileIDs, parsed.Profiles)
	if err != nil {
		return err
	}
	rollbackProfiles := func(applyErr error) error {
		if restoreErr := restoreFile(profileStore.Path(), profileSnapshot, profileExisted); restoreErr != nil {
			return fmt.Errorf("subscription update failed after profile apply: %w; additionally failed to restore profile store: %v", applyErr, restoreErr)
		}
		return applyErr
	}
	if subscriptionAfterProfileApplyHook != nil {
		if err := subscriptionAfterProfileApplyHook(); err != nil {
			return rollbackProfiles(err)
		}
	}
	source.Format = format
	source.ProfileIDs = profileIDs(parsed.Profiles)
	source.LastUpdatedAt = time.Now().UTC()
	if err := store.Update(source); err != nil {
		return rollbackProfiles(err)
	}
	result := sub.UpdateResult{
		Subscription: source,
		Imported:     diff.Imported,
		Updated:      diff.Updated,
		Unchanged:    diff.Unchanged,
		Removed:      diff.Removed,
		Unsupported:  len(parsed.Unsupported),
		Warnings:     parsed.Warnings,
		Issues:       parsed.Unsupported,
	}
	printSubscriptionUpdateResult(stdout, result)
	return nil
}

func profileIDs(profiles []profile.Profile) []string {
	ids := make([]string, len(profiles))
	for i, p := range profiles {
		ids[i] = p.ID
	}
	return ids
}
