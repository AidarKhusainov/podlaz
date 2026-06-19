package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/render"
	"github.com/AidarKhusainov/podlaz/internal/sub"
)

type subscriptionOutput struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	URL           string     `json:"url"`
	Format        sub.Format `json:"format"`
	ProfileIDs    []string   `json:"profile_ids,omitempty"`
	LastUpdatedAt string     `json:"last_updated_at,omitempty"`
}

func subscriptionsForOutput(sources []sub.Source) []subscriptionOutput {
	out := make([]subscriptionOutput, len(sources))
	for i, source := range sources {
		out[i] = subscriptionForOutput(source)
	}
	return out
}

func subscriptionForOutput(source sub.Source) subscriptionOutput {
	out := subscriptionOutput{
		ID:         render.Redact(source.ID),
		Name:       render.Redact(source.Name),
		URL:        "REDACTED",
		Format:     source.Format,
		ProfileIDs: make([]string, len(source.ProfileIDs)),
	}
	for i, id := range source.ProfileIDs {
		out.ProfileIDs[i] = render.Redact(id)
	}
	if !source.LastUpdatedAt.IsZero() {
		out.LastUpdatedAt = source.LastUpdatedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func printSubscriptionUpdateResult(stdout io.Writer, result sub.UpdateResult) {
	out := subscriptionForOutput(result.Subscription)
	fmt.Fprintf(stdout, "Subscription updated: %s\n", out.ID)
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
