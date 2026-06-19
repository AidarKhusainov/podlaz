package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/render"
	"github.com/AidarKhusainov/podlaz/internal/sub"
)

func runSubscriptionImport(ctx context.Context, sourceURL string, stdout io.Writer, opts options) error {
	storePath, err := resolvedSubscriptionStorePath(opts)
	if err != nil {
		return err
	}
	subscriptionStore, err := sub.NewStore(storePath)
	if err != nil {
		return err
	}
	profileStore, err := profile.NewStore(opts.profileStorePath)
	if err != nil {
		return err
	}

	source := sub.NewSource("", sourceURL)
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

	subscriptionSnapshot, subscriptionExisted, err := snapshotFile(subscriptionStore.Path())
	if err != nil {
		return err
	}
	if err := subscriptionStore.Add(source); err != nil {
		return subscriptionCommandError(err)
	}
	rollbackSubscription := func(applyErr error) error {
		if restoreErr := restoreFile(subscriptionStore.Path(), subscriptionSnapshot, subscriptionExisted); restoreErr != nil {
			return fmt.Errorf("import failed after subscription apply: %w; additionally failed to restore subscription store: %v", applyErr, restoreErr)
		}
		return applyErr
	}

	profileSnapshot, profileExisted, err := snapshotFile(profileStore.Path())
	if err != nil {
		return rollbackSubscription(err)
	}
	diff, err := profileStore.ReplaceSubscriptionProfiles(nil, parsed.Profiles)
	if err != nil {
		return rollbackSubscription(err)
	}
	rollbackProfilesAndSubscription := func(applyErr error) error {
		var restoreMessages []string
		if restoreErr := restoreFile(profileStore.Path(), profileSnapshot, profileExisted); restoreErr != nil {
			restoreMessages = append(restoreMessages, fmt.Sprintf("failed to restore profile store: %v", restoreErr))
		}
		if restoreErr := restoreFile(subscriptionStore.Path(), subscriptionSnapshot, subscriptionExisted); restoreErr != nil {
			restoreMessages = append(restoreMessages, fmt.Sprintf("failed to restore subscription store: %v", restoreErr))
		}
		if len(restoreMessages) > 0 {
			return fmt.Errorf("import failed after profile apply: %w; additionally %s", applyErr, strings.Join(restoreMessages, "; "))
		}
		return applyErr
	}

	if subscriptionAfterProfileApplyHook != nil {
		if err := subscriptionAfterProfileApplyHook(); err != nil {
			return rollbackProfilesAndSubscription(err)
		}
	}
	source.Format = format
	source.ProfileIDs = profileIDs(parsed.Profiles)
	source.LastUpdatedAt = time.Now().UTC()
	if err := subscriptionStore.Update(source); err != nil {
		return rollbackProfilesAndSubscription(err)
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
	printSubscriptionImportResult(stdout, result)
	return nil
}

func printSubscriptionImportResult(stdout io.Writer, result sub.UpdateResult) {
	out := subscriptionForOutput(result.Subscription)
	fmt.Fprintf(stdout, "Subscription imported: %s\n", out.ID)
	fmt.Fprintf(stdout, "Name: %s\n", out.Name)
	fmt.Fprintf(stdout, "Format: %s\n", result.Subscription.Format)
	fmt.Fprintf(stdout, "Imported: %d\n", result.Imported)
	fmt.Fprintf(stdout, "Updated: %d\n", result.Updated)
	fmt.Fprintf(stdout, "Unchanged: %d\n", result.Unchanged)
	fmt.Fprintf(stdout, "Removed: %d\n", result.Removed)
	fmt.Fprintf(stdout, "Unsupported: %d\n", result.Unsupported)
	fmt.Fprintf(stdout, "Warnings: %d\n", len(result.Warnings))
	if len(result.Issues) > 0 {
		fmt.Fprintln(stdout, "Unsupported entries:")
		for _, issue := range result.Issues {
			fmt.Fprintf(stdout, "- line %d: %s\n", issue.Line, render.Redact(issue.Message))
		}
	}
	if len(result.Warnings) > 0 {
		fmt.Fprintln(stdout, "Warning details:")
		for _, warning := range result.Warnings {
			fmt.Fprintf(stdout, "- line %d: %s\n", warning.Line, render.Redact(warning.Message))
		}
	}
}
