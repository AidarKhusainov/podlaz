package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/render"
	"github.com/AidarKhusainov/podlaz/internal/sub"
)

type subscriptionDeleteArgs struct {
	id           string
	yes          bool
	keepProfiles bool
}

func runSubscriptionDelete(store sub.Store, profileStore profile.Store, args []string, stdout io.Writer, opts options) error {
	parsed, err := parseSubscriptionDeleteArgs(args)
	if err != nil {
		return err
	}

	source, err := store.Get(parsed.id)
	if err != nil {
		return subscriptionCommandError(err)
	}
	profileCount := len(source.ProfileIDs)

	if !parsed.yes {
		if err := confirmSubscriptionDelete(stdout, opts, source, parsed.keepProfiles); err != nil {
			return err
		}
	}

	if parsed.keepProfiles {
		if err := store.Delete(source.ID); err != nil {
			return subscriptionCommandError(err)
		}
		fmt.Fprintf(stdout, "Subscription deleted: %s\n", render.Redact(source.ID))
		fmt.Fprintf(stdout, "Profiles kept: %d\n", profileCount)
		return nil
	}

	linkedProfileIDs, err := subscriptionProfileIDsReferencedByOtherSources(store, source.ID)
	if err != nil {
		return err
	}
	matchingProfiles, err := profileStore.CountUnlinkedProfilesMatchingSubscriptionServers(source.ProfileIDs, linkedProfileIDs)
	if err != nil {
		return err
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
	if matchingProfiles > 0 {
		fmt.Fprintf(stdout, "Profiles with matching servers were left untouched: %d\n", matchingProfiles)
	}
	return nil
}

func subscriptionProfileIDsReferencedByOtherSources(store sub.Store, deletedSourceID string) ([]string, error) {
	sources, err := store.List()
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, source := range sources {
		if source.ID == deletedSourceID {
			continue
		}
		ids = append(ids, source.ProfileIDs...)
	}
	return ids, nil
}

func confirmSubscriptionDelete(stdout io.Writer, opts options, source sub.Source, keepProfiles bool) error {
	if !subscriptionDeleteInputIsTerminal(opts) {
		return usageError("subscription delete requires --yes in non-interactive mode")
	}
	action := "remove"
	if keepProfiles {
		action = "keep"
	}
	fmt.Fprintf(stdout, "Delete subscription %s and %s %d imported profiles? Type yes to continue: ", render.Redact(source.ID), action, len(source.ProfileIDs))
	reader := opts.stdin
	if reader == nil {
		reader = os.Stdin
	}
	line, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read subscription delete confirmation: %w", err)
	}
	if strings.EqualFold(strings.TrimSpace(line), "yes") {
		return nil
	}
	return exitError{code: 1, err: errors.New("subscription delete canceled")}
}

func subscriptionDeleteInputIsTerminal(opts options) bool {
	if opts.stdinIsTerminal != nil {
		return opts.stdinIsTerminal()
	}
	return isStdinTerminal()
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
