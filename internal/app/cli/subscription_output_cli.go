package cli

import (
	"fmt"
	"io"

	"github.com/AidarKhusainov/podlaz/internal/render"
	"github.com/AidarKhusainov/podlaz/internal/sub"
)

func redactedSubscriptionID(source sub.Source) string {
	return render.Redact(source.ID)
}

func redactedSubscriptionName(source sub.Source) string {
	return render.Redact(source.Name)
}

func printSubscriptionUpdateResult(stdout io.Writer, result sub.UpdateResult) {
	fmt.Fprintf(stdout, "Subscription updated: %s\n", redactedSubscriptionID(result.Subscription))
	fmt.Fprintf(stdout, "Name: %s\n", redactedSubscriptionName(result.Subscription))
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
