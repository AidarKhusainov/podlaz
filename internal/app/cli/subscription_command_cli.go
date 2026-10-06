package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/sub"
)

var subscriptionAfterProfileApplyHook func() error

func runSubscriptionCommand(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	if isHelp(args) {
		printSubscriptionHelp(stdout)
		return nil
	}
	if len(args) == 0 {
		return usageError("subscription requires a subcommand")
	}

	storePath, err := resolvedSubscriptionStorePath(opts)
	if err != nil {
		return err
	}
	store, err := sub.NewStore(storePath)
	if err != nil {
		return err
	}

	switch strings.ToLower(args[0]) {
	case "list":
		return runSubscriptionList(store, args[1:], stdout)
	case "show":
		return runSubscriptionShow(store, args[1:], stdout)
	case "update":
		profileStore, err := profile.NewStore(opts.profileStorePath)
		if err != nil {
			return err
		}
		return runSubscriptionUpdate(ctx, store, profileStore, args[1:], stdout)
	case "delete":
		profileStore, err := profile.NewStore(opts.profileStorePath)
		if err != nil {
			return err
		}
		return runSubscriptionDelete(store, profileStore, args[1:], stdout, opts)
	default:
		return usageError("unknown subscription subcommand %q", args[0])
	}
}

func runSubscriptionList(store sub.Store, args []string, stdout io.Writer) error {
	if len(args) != 0 {
		return usageError("subscription list does not accept arguments")
	}
	sources, err := store.List()
	if err != nil {
		return err
	}
	rows := make([][]string, 0, len(sources))
	for _, source := range sources {
		out := subscriptionForOutput(source)
		updated := "never"
		if !source.LastUpdatedAt.IsZero() {
			updated = source.LastUpdatedAt.UTC().Format(time.RFC3339)
		}
		rows = append(rows, []string{out.ID, out.Name, string(out.Format), strconv.Itoa(len(source.ProfileIDs)), updated})
	}
	return writeTable(stdout, []string{"ID", "NAME", "FORMAT", "PROFILES", "UPDATED"}, rows)
}

func runSubscriptionShow(store sub.Store, args []string, stdout io.Writer) error {
	id, err := parseSubscriptionShowArgs(args)
	if err != nil {
		return err
	}
	source, err := store.Get(id)
	if err != nil {
		return subscriptionCommandError(err)
	}
	out := subscriptionForOutput(source)
	fmt.Fprintf(stdout, "ID: %s\n", out.ID)
	fmt.Fprintf(stdout, "Name: %s\n", out.Name)
	fmt.Fprintf(stdout, "Format: %s\n", out.Format)
	fmt.Fprintf(stdout, "Imported profiles: %d\n", len(source.ProfileIDs))
	if source.LastUpdatedAt.IsZero() {
		fmt.Fprintln(stdout, "Last updated: never")
	} else {
		fmt.Fprintf(stdout, "Last updated: %s\n", source.LastUpdatedAt.UTC().Format(time.RFC3339))
	}
	return nil
}

func printSubscriptionHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  podlaz subscription list
  podlaz subscription show <subscription-id>
  podlaz subscription update <subscription-id>
  podlaz subscription delete <subscription-id> [--yes] [--keep-profiles]

Normal onboarding uses "podlaz import <uri|url|file>". Subscription commands only
manage an already imported source. Source URLs and credentials are never printed.
Deletion removes subscription-owned profiles by exact persisted ownership unless
--keep-profiles is explicitly requested.
`)
}
