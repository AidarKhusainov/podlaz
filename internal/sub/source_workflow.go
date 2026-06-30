package sub

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/profile"
)

// SourceWorkflowOptions configures subscription import/update side effects that
// need to be injectable for deterministic tests.
type SourceWorkflowOptions struct {
	AfterProfileApply func() error
	Now               func() time.Time
}

// ImportSource fetches, parses, persists, and links a new subscription source
// through the same user-state workflow used by subscription updates.
func ImportSource(ctx context.Context, store Store, profileStore profile.Store, sourceURL string, opts SourceWorkflowOptions) (UpdateResult, error) {
	source := NewSource("", sourceURL)
	return runSourceWorkflow(ctx, sourceWorkflowRequest{
		store:              store,
		profileStore:       profileStore,
		source:             source,
		previousProfileIDs: nil,
		addSource:          true,
		options:            opts,
	})
}

// UpdateSource fetches, parses, replaces profiles, and refreshes metadata for an
// existing subscription source through the shared source workflow.
func UpdateSource(ctx context.Context, store Store, profileStore profile.Store, id string, opts SourceWorkflowOptions) (UpdateResult, error) {
	source, err := store.Get(id)
	if err != nil {
		return UpdateResult{}, err
	}
	return runSourceWorkflow(ctx, sourceWorkflowRequest{
		store:              store,
		profileStore:       profileStore,
		source:             source,
		previousProfileIDs: source.ProfileIDs,
		options:            opts,
	})
}

type sourceWorkflowRequest struct {
	store              Store
	profileStore       profile.Store
	source             Source
	previousProfileIDs []string
	addSource          bool
	options            SourceWorkflowOptions
}

type preparedSource struct {
	source Source
	format Format
	parsed Parsed
}

func runSourceWorkflow(ctx context.Context, req sourceWorkflowRequest) (UpdateResult, error) {
	prepared, err := prepareSource(ctx, req.source)
	if err != nil {
		return UpdateResult{}, err
	}
	source := prepared.source

	subscriptionSnapshot, err := snapshotSourceWorkflowFile(req.store.Path())
	if err != nil {
		return UpdateResult{}, err
	}
	if req.addSource {
		if err := req.store.Add(source); err != nil {
			return UpdateResult{}, err
		}
	}

	profileSnapshot, err := snapshotSourceWorkflowFile(req.profileStore.Path())
	if err != nil {
		if req.addSource {
			return UpdateResult{}, rollbackSourceWorkflowState(err, subscriptionSnapshot)
		}
		return UpdateResult{}, err
	}
	diff, err := req.profileStore.ReplaceSubscriptionProfiles(req.previousProfileIDs, prepared.parsed.Profiles)
	if err != nil {
		if req.addSource {
			return UpdateResult{}, rollbackSourceWorkflowState(err, profileSnapshot, subscriptionSnapshot)
		}
		return UpdateResult{}, rollbackSourceWorkflowState(err, profileSnapshot)
	}

	rollbackUserState := func(applyErr error) error {
		return rollbackSourceWorkflowState(applyErr, profileSnapshot, subscriptionSnapshot)
	}
	if req.options.AfterProfileApply != nil {
		if err := req.options.AfterProfileApply(); err != nil {
			return UpdateResult{}, rollbackUserState(err)
		}
	}

	source.Format = prepared.format
	source.ProfileIDs = sourceWorkflowProfileIDs(prepared.parsed.Profiles)
	source.LastUpdatedAt = sourceWorkflowNow(req.options).UTC()
	if err := req.store.Update(source); err != nil {
		return UpdateResult{}, rollbackUserState(err)
	}

	return UpdateResult{
		Subscription: source,
		Imported:     diff.Imported,
		Updated:      diff.Updated,
		Unchanged:    diff.Unchanged,
		Removed:      diff.Removed,
		Unsupported:  len(prepared.parsed.Unsupported),
		Warnings:     prepared.parsed.Warnings,
		Issues:       prepared.parsed.Unsupported,
	}, nil
}

func prepareSource(ctx context.Context, source Source) (preparedSource, error) {
	fetchResult, err := FetchSourceWithMetadata(ctx, source)
	if err != nil {
		return preparedSource{}, err
	}
	format, parsed, err := ParseSubscriptionContent(fetchResult.Content)
	if err != nil {
		return preparedSource{}, err
	}
	providerName, providerNameWarnings := ProviderSubscriptionDisplayNameFromMetadata(format, fetchResult.Content, fetchResult.Header)
	parsed.Warnings = append(parsed.Warnings, providerNameWarnings...)
	return preparedSource{
		source: RefreshProviderDisplayName(source, providerName),
		format: format,
		parsed: parsed,
	}, nil
}

func sourceWorkflowNow(opts SourceWorkflowOptions) time.Time {
	if opts.Now != nil {
		return opts.Now()
	}
	return time.Now()
}

func sourceWorkflowProfileIDs(profiles []profile.Profile) []string {
	ids := make([]string, len(profiles))
	for i, p := range profiles {
		ids[i] = p.ID
	}
	return ids
}

type sourceWorkflowFileSnapshot struct {
	path    string
	data    []byte
	existed bool
}

func snapshotSourceWorkflowFile(path string) (sourceWorkflowFileSnapshot, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return sourceWorkflowFileSnapshot{path: path}, nil
	}
	if err != nil {
		return sourceWorkflowFileSnapshot{}, fmt.Errorf("snapshot file %s: %w", path, err)
	}
	return sourceWorkflowFileSnapshot{path: path, data: data, existed: true}, nil
}

func rollbackSourceWorkflowState(applyErr error, snapshots ...sourceWorkflowFileSnapshot) error {
	var restoreMessages []string
	for _, snapshot := range snapshots {
		if err := restoreSourceWorkflowFile(snapshot); err != nil {
			restoreMessages = append(restoreMessages, err.Error())
		}
	}
	if len(restoreMessages) > 0 {
		return fmt.Errorf("source workflow failed after user-state mutation: %w; additionally %s", applyErr, strings.Join(restoreMessages, "; "))
	}
	return applyErr
}

func restoreSourceWorkflowFile(snapshot sourceWorkflowFileSnapshot) error {
	if !snapshot.existed {
		if err := os.Remove(snapshot.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove newly created file %s: %w", snapshot.path, err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(snapshot.path), 0o700); err != nil {
		return fmt.Errorf("create restore directory for %s: %w", snapshot.path, err)
	}
	if err := os.WriteFile(snapshot.path, snapshot.data, 0o600); err != nil {
		return fmt.Errorf("restore file %s: %w", snapshot.path, err)
	}
	return nil
}
