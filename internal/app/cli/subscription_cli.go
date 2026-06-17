package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AidarKhusainov/tunwarden/internal/profile"
	"github.com/AidarKhusainov/tunwarden/internal/render"
	"github.com/AidarKhusainov/tunwarden/internal/sub"
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
	case "add":
		return runSubscriptionAdd(store, args[1:], stdout)
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

func runSubscriptionAdd(store sub.Store, args []string, stdout io.Writer) error {
	parsed, err := parseSubscriptionAddArgs(args)
	if err != nil {
		return err
	}
	source := sub.NewSource(parsed.name, parsed.url)
	if err := store.Add(source); err != nil {
		return subscriptionCommandError(err)
	}
	fmt.Fprintf(stdout, "Subscription added: %s\n", render.Redact(source.ID))
	return nil
}

func runSubscriptionList(store sub.Store, args []string, stdout io.Writer) error {
	jsonOutput, err := parseOptionalJSON(args, "subscription list")
	if err != nil {
		return err
	}
	sources, err := store.List()
	if err != nil {
		return err
	}
	if jsonOutput {
		return writeJSON(stdout, okJSON(map[string]any{"subscriptions": subscriptionsForOutput(sources)}))
	}
	fmt.Fprintln(stdout, "ID        NAME   FORMAT  PROFILES  UPDATED")
	for _, source := range sources {
		out := subscriptionForOutput(source)
		updated := "never"
		if !source.LastUpdatedAt.IsZero() {
			updated = source.LastUpdatedAt.UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(stdout, "%-9s %-6s %-7s %-8d %s\n", out.ID, out.Name, out.Format, len(source.ProfileIDs), updated)
	}
	return nil
}

func runSubscriptionShow(store sub.Store, args []string, stdout io.Writer) error {
	id, jsonOutput, err := parseSubscriptionShowArgs(args)
	if err != nil {
		return err
	}
	source, err := store.Get(id)
	if err != nil {
		return subscriptionCommandError(err)
	}
	out := subscriptionForOutput(source)
	if jsonOutput {
		return writeJSON(stdout, okJSON(map[string]any{"subscription": out}))
	}
	fmt.Fprintf(stdout, "ID: %s\n", out.ID)
	fmt.Fprintf(stdout, "Name: %s\n", out.Name)
	fmt.Fprintf(stdout, "URL: %s\n", out.URL)
	fmt.Fprintf(stdout, "Format: %s\n", out.Format)
	fmt.Fprintf(stdout, "Imported profiles: %d\n", len(source.ProfileIDs))
	if source.LastUpdatedAt.IsZero() {
		fmt.Fprintln(stdout, "Last updated: never")
	} else {
		fmt.Fprintf(stdout, "Last updated: %s\n", source.LastUpdatedAt.UTC().Format(time.RFC3339))
	}
	return nil
}

func runSubscriptionUpdate(ctx context.Context, store sub.Store, profileStore profile.Store, args []string, stdout io.Writer) error {
	id, err := parseSubscriptionUpdateArgs(args)
	if err != nil {
		return err
	}
	source, err := store.Get(id)
	if err != nil {
		return subscriptionCommandError(err)
	}
	content, err := sub.FetchSource(ctx, source)
	if err != nil {
		return err
	}
	format, parsed, err := sub.ParseSubscriptionContent(content)
	if err != nil {
		return err
	}
	profileSnapshot, profileExisted, err := snapshotFile(profileStore.Path())
	if err != nil {
		return err
	}
	diff, err := profileStore.ReplaceSubscriptionProfiles(source.ProfileIDs, parsed.Profiles)
	if err != nil {
		return err
	}
	rollbackProfiles := func(applyErr error) error {
		if restoreErr := restoreFile(profileStore.Path(), profileSnapshot, profileExisted); restoreErr != nil {
			return fmt.Errorf("subscription update failed after profile apply: %w; additionally failed to restore profile store: %v", applyErr, restoreErr)
		}
		return applyErr
	}
	if subscriptionAfterProfileApplyHook != nil {
		if err := subscriptionAfterProfileApplyHook(); err != nil {
			return rollbackProfiles(err)
		}
	}
	source.Format = format
	source.ProfileIDs = profileIDs(parsed.Profiles)
	source.LastUpdatedAt = time.Now().UTC()
	if err := store.Update(source); err != nil {
		return rollbackProfiles(err)
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
	printSubscriptionUpdateResult(stdout, result)
	return nil
}

type subscriptionAddArgs struct {
	name string
	url  string
}

func parseSubscriptionAddArgs(args []string) (subscriptionAddArgs, error) {
	var parsed subscriptionAddArgs
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value, hasInlineValue := cutFlagValue(arg)
		switch {
		case arg == "--name" || strings.HasPrefix(arg, "--name="):
			v, next, err := flagValue("subscription add --name", args, i, value, hasInlineValue)
			if err != nil {
				return parsed, err
			}
			parsed.name = v
			i = next
		case arg == "--url" || strings.HasPrefix(arg, "--url="):
			v, next, err := flagValue("subscription add --url", args, i, value, hasInlineValue)
			if err != nil {
				return parsed, err
			}
			parsed.url = v
			i = next
		case arg == "--json":
			return parsed, usageError("subscription add --json is not implemented")
		default:
			return parsed, usageError("unsupported subscription add argument %q", arg)
		}
	}
	if err := sub.ValidateSource(sub.NewSource(parsed.name, parsed.url)); err != nil {
		return parsed, usageError("%s", err.Error())
	}
	return parsed, nil
}

func parseSubscriptionShowArgs(args []string) (string, bool, error) {
	var id string
	var jsonOutput bool
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOutput = true
		default:
			if strings.HasPrefix(arg, "-") {
				return "", false, usageError("unsupported subscription show argument %q", arg)
			}
			if id != "" {
				return "", false, usageError("subscription show accepts exactly one subscription id")
			}
			id = arg
		}
	}
	if id == "" {
		return "", false, usageError("subscription show requires a subscription id")
	}
	return id, jsonOutput, nil
}

func parseSubscriptionUpdateArgs(args []string) (string, error) {
	var id string
	for _, arg := range args {
		switch arg {
		case "--json":
			return "", usageError("subscription update --json is not implemented")
		default:
			if strings.HasPrefix(arg, "-") {
				return "", usageError("unsupported subscription update argument %q", arg)
			}
			if id != "" {
				return "", usageError("subscription update accepts exactly one subscription id")
			}
			id = arg
		}
	}
	if id == "" {
		return "", usageError("subscription update requires a subscription id")
	}
	return id, nil
}

func subscriptionCommandError(err error) error {
	switch {
	case errors.Is(err, sub.ErrNotFound):
		return exitError{code: 1, err: err}
	case errors.Is(err, sub.ErrAlreadyExists):
		return exitError{code: 1, err: err}
	default:
		return err
	}
}

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
	fmt.Fprintf(stdout, "Subscription updated: %s\n", render.Redact(result.Subscription.ID))
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

func profileIDs(profiles []profile.Profile) []string {
	ids := make([]string, len(profiles))
	for i, p := range profiles {
		ids[i] = p.ID
	}
	return ids
}

func snapshotFile(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("snapshot file %s: %w", path, err)
	}
	return data, true, nil
}

func restoreFile(path string, data []byte, existed bool) error {
	if !existed {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove newly created file %s: %w", path, err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create restore directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("restore file %s: %w", path, err)
	}
	return nil
}

func resolvedSubscriptionStorePath(opts options) (string, error) {
	if opts.subscriptionStorePath != "" {
		return opts.subscriptionStorePath, nil
	}
	if opts.profileStorePath != "" {
		return filepath.Join(filepath.Dir(opts.profileStorePath), "subscriptions.json"), nil
	}
	return "", nil
}

func printSubscriptionHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  tunwarden subscription add --name <name> --url <file-or-http-url>
  tunwarden subscription update <subscription-id>
  tunwarden subscription list [--json]
  tunwarden subscription show <subscription-id> [--json]
  tunwarden subscription delete <subscription-id> [--yes] [--keep-profiles]

Manage subscription sources and imported subscription profiles in local
TunWarden user state.

Supported subscription sources:
  file/http/https

Supported subscription formats:
  Base64 URI-list, Xray JSON

Delete behavior:
  subscription delete removes subscription metadata and, by default, profiles
  owned by that subscription. Use --keep-profiles to remove only subscription
  metadata and leave imported profiles in the profile store.
`)
}
