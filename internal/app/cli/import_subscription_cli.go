package cli

import (
	"context"
	"fmt"
	"io"

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

	result, err := sub.ImportSource(ctx, subscriptionStore, profileStore, sourceURL, sub.SourceWorkflowOptions{
		AfterProfileApply: subscriptionAfterProfileApplyHook,
	})
	if err != nil {
		return subscriptionCommandError(err)
	}
	if len(result.Subscription.ProfileIDs) == 1 {
		if _, err := profileStore.SelectIfUnset(result.Subscription.ProfileIDs[0]); err != nil {
			return err
		}
	}
	return printSubscriptionImportResult(stdout, profileStore, result)
}

func printSubscriptionImportResult(stdout io.Writer, store profile.Store, result sub.UpdateResult) error {
	name := redactedSubscriptionName(result.Subscription)
	fmt.Fprintln(stdout, "Subscription imported")
	fmt.Fprintf(stdout, "Name: %s\n", name)
	fmt.Fprintf(stdout, "Profiles: %d\n", len(result.Subscription.ProfileIDs))
	if result.Unsupported > 0 {
		fmt.Fprintf(stdout, "Skipped unsupported entries: %d\n", result.Unsupported)
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(stdout, "Warning: %s\n", render.Redact(warning.Message))
	}
	selectedID, err := store.SelectedID()
	if err != nil {
		return err
	}
	if selectedID == "" {
		fmt.Fprintln(stdout, "Next: podlaz profile use <profile>")
		return nil
	}
	fmt.Fprintln(stdout, "Next: podlaz connect")
	return nil
}
