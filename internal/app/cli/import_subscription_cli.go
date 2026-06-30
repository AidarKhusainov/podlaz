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
