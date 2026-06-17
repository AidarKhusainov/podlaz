package cli

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/AidarKhusainov/tunwarden/internal/profile"
	"github.com/AidarKhusainov/tunwarden/internal/render"
	"github.com/AidarKhusainov/tunwarden/internal/sub"
)

func runImportCommand(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	if isHelp(args) {
		printImportHelp(stdout)
		return nil
	}

	target, err := parseImportArgs(args)
	if err != nil {
		return err
	}

	u, err := url.Parse(target)
	if err != nil {
		return usageError("invalid import target: malformed URI or URL")
	}
	if u.Scheme == "" {
		return runLocalFileImport(target, stdout, opts)
	}

	switch strings.ToLower(u.Scheme) {
	case "vless", "vmess", "trojan", "ss":
		store, err := profile.NewStore(opts.profileStorePath)
		if err != nil {
			return err
		}
		return runProfileImport(store, []string{target}, stdout)
	case "file", "http", "https":
		return runSubscriptionImport(ctx, target, stdout, opts)
	default:
		return usageError("unsupported import scheme %q", u.Scheme)
	}
}

func parseImportArgs(args []string) (string, error) {
	var target string
	for _, arg := range args {
		switch arg {
		case "--json":
			return "", usageError("import --json is not implemented")
		default:
			if strings.HasPrefix(arg, "-") {
				return "", usageError("unsupported import argument %q", arg)
			}
			if target != "" {
				return "", usageError("import accepts exactly one URI, URL, or local path")
			}
			target = arg
		}
	}
	if strings.TrimSpace(target) == "" {
		return "", usageError("import requires a URI, URL, or local path")
	}
	return target, nil
}

func runLocalFileImport(path string, stdout io.Writer, opts options) error {
	content, err := profile.ReadLocalImportFile(path)
	if err != nil {
		return err
	}
	result, err := profile.ImportLocalContent(content)
	if err != nil {
		return usageError("%s", render.Redact(err.Error()))
	}
	store, err := profile.NewStore(opts.profileStorePath)
	if err != nil {
		return err
	}
	if err := store.AddProfiles(result.Profiles); err != nil {
		return profileCommandError(err)
	}
	printLocalImportResult(stdout, result)
	return nil
}

func printLocalImportResult(stdout io.Writer, result profile.LocalImportResult) {
	fmt.Fprintln(stdout, "Local import completed")
	fmt.Fprintf(stdout, "Format: %s\n", result.Format)
	fmt.Fprintf(stdout, "Inspected: %d\n", result.Inspected)
	fmt.Fprintf(stdout, "Imported: %d\n", len(result.Profiles))
	fmt.Fprintf(stdout, "Skipped: %d\n", len(result.Unsupported))
	fmt.Fprintf(stdout, "Warnings: %d\n", len(result.Warnings))
	if len(result.Profiles) > 0 {
		fmt.Fprintln(stdout, "Imported profiles:")
		for _, p := range result.Profiles {
			fmt.Fprintf(stdout, "- %s %s\n", render.Redact(p.ID), render.Redact(p.Name))
		}
	}
	if len(result.Unsupported) > 0 {
		fmt.Fprintln(stdout, "Skipped entries:")
		for _, issue := range result.Unsupported {
			fmt.Fprintf(stdout, "- entry %d: %s\n", issue.Entry, render.Redact(issue.Message))
		}
	}
	if len(result.Warnings) > 0 {
		fmt.Fprintln(stdout, "Warning details:")
		for _, warning := range result.Warnings {
			fmt.Fprintf(stdout, "- entry %d: %s\n", warning.Entry, render.Redact(warning.Message))
		}
	}
}

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
	content, err := sub.FetchSource(ctx, source)
	if err != nil {
		return err
	}
	format, parsed, err := sub.ParseSubscriptionContent(content)
	if err != nil {
		return err
	}
	providerName, providerNameWarnings := sub.ProviderSubscriptionDisplayName(format, content)
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

func printImportHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  tunwarden import <share-uri>
  tunwarden import <local-path>
  tunwarden import <subscription-url>

Import a supported share URI, local import file, or subscription URL into
user-owned TunWarden state.

Supported local files:
  Xray JSON, plain URI-list, Base64 URI-list

Supported subscription URLs:
  Base64 URI-list and Xray JSON over file/http/https

Examples:
  tunwarden import 'vless://...'
  tunwarden import ./profiles.json
  tunwarden import https://example.com/subscription
`)
}
