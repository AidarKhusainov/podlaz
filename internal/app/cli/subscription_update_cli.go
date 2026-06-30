package cli

import (
	"context"
	"io"

	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/sub"
)

func runSubscriptionUpdate(ctx context.Context, store sub.Store, profileStore profile.Store, args []string, stdout io.Writer) error {
	id, err := parseSubscriptionUpdateArgs(args)
	if err != nil {
		return err
	}
	result, err := sub.UpdateSource(ctx, store, profileStore, id, sub.SourceWorkflowOptions{
		AfterProfileApply: subscriptionAfterProfileApplyHook,
	})
	if err != nil {
		return subscriptionCommandError(err)
	}
	printSubscriptionUpdateResult(stdout, result)
	return nil
}
