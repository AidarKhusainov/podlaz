package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/AidarKhusainov/tunwarden/internal/profile"
	"github.com/AidarKhusainov/tunwarden/internal/render"
	"github.com/AidarKhusainov/tunwarden/internal/sub"
)

type subscriptionDeleteArgs struct {
	id           string
	yes          bool
	keepProfiles bool
}

func runSubscriptionDelete(store sub.Store, profileStore profile.Store, args []string, stdout io.Writer) error {
	parsed, err := parseSubscriptionDeleteArgs(args)
	if err != nil {
		return err
	}
	if !parsed.yes {
		return usageError("subscription delete requires --yes in this non-interactive v0.1 CLI")
	}

	source, err := store.Get(parsed.id)
	if err != nil {
		return subscriptionCommandError(err)
	}
	profileCount := len(source.ProfileIDs)

	if parsed.keepProfiles {
		if err := store.Delete(source.ID); err != nil {
			return subscriptionCommandError(err)
		}
		fmt.Fprintf(stdout, "Subscription deleted: %s\n", render.Redact(source.ID))
		fmt.Fprintf(stdout, "Profiles kept: %d\n", profileCount)
		return nil
	}

	profileSnapshot, profileExisted, err := snapshotFile(profileStore.Path())
	if err != nil {
		return err
	}
	removedProfiles, err := profileStore.DeleteSubscriptionProfiles(source.ProfileIDs)
	if err != nil {
		return err
	}
	rollbackProfiles := func(applyErr error) error {
		if restoreErr := restoreFile(profileStore.Path(), profileSnapshot, profileExisted); restoreErr != nil {
			return fmt.Errorf("subscription delete failed after profile cleanup: %w; additionally failed to restore profile store: %v", applyErr, restoreErr)
		}
		return applyErr
	}
	if subscriptionAfterProfileApplyHook != nil {
		if err := subscriptionAfterProfileApplyHook(); err != nil {
			return rollbackProfiles(err)
		}
	}
	if err := store.Delete(source.ID); err != nil {
		return rollbackProfiles(subscriptionCommandError(err))
	}

	fmt.Fprintf(stdout, "Subscription deleted: %s\n", render.Redact(source.ID))
	fmt.Fprintf(stdout, "Profiles removed: %d\n", removedProfiles)
	return nil
}

func parseSubscriptionDeleteArgs(args []string) (subscriptionDeleteArgs, error) {
	var parsed subscriptionDeleteArgs
	for _, arg := range args {
		switch arg {
		case "--yes":
			parsed.yes = true
		case "--keep-profiles":
			parsed.keepProfiles = true
		case "--json":
			return parsed, usageError("subscription delete --json is not implemented")
		default:
			if strings.HasPrefix(arg, "-") {
				return parsed, usageError("unsupported subscription delete argument %q", arg)
			}
			if parsed.id != "" {
				return parsed, usageError("subscription delete accepts exactly one subscription id")
			}
			parsed.id = arg
		}
	}
	if parsed.id == "" {
		return parsed, usageError("subscription delete requires a subscription id")
	}
	return parsed, nil
}
