package cli

import (
	"context"
	"crypto/sha256"
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
		return usageError("invalid import target: %s", err.Error())
	}
	if u.Scheme == "" {
		return usageError("import requires a VLESS share URI or a file/http/https subscription URL")
	}

	switch strings.ToLower(u.Scheme) {
	case "vless":
		store, err := profile.NewStore(opts.profileStorePath)
		if err != nil {
			return err
		}
		return runProfileImport(store, []string{target}, stdout)
	case "file", "http", "https":
		return runSubscriptionImport(ctx, target, stdout, opts)
	default:
		return usageError("unsupported import scheme %q: supported imports are vless:// share URIs and file/http/https Base64 subscriptions", u.Scheme)
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
				return "", usageError("import accepts exactly one URI or subscription URL")
			}
			target = arg
		}
	}
	if strings.TrimSpace(target) == "" {
		return "", usageError("import requires a URI or subscription URL")
	}
	return target, nil
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

	source := sub.NewSource(importedSubscriptionName(sourceURL), sourceURL)
	content, err := sub.FetchSource(ctx, source)
	if err != nil {
		return err
	}
	parsed, err := sub.ParseBase64Subscription(content)
	if err != nil {
		return err
	}

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

func importedSubscriptionName(rawURL string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(rawURL)))
	return fmt.Sprintf("imported subscription %x", sum[:4])
}

func printSubscriptionImportResult(stdout io.Writer, result sub.UpdateResult) {
	fmt.Fprintf(stdout, "Subscription imported: %s\n", render.Redact(result.Subscription.ID))
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
  tunwarden import <vless-share-uri>
  tunwarden import <file-or-http-subscription-url>

Import one supported profile or subscription through the first-run convenience
entrypoint. VLESS share URIs are stored as imported profiles. file/http/https
subscription URLs are fetched as Base64 URI-list subscriptions, imported into the
profile store, and tracked as subscription-owned profiles.

Implemented in v0.1:
  VLESS share URI import, file/http/https Base64 URI-list subscription import,
  supported VLESS subscription entries, unsupported entry reporting, and
  last-known-good rollback when subscription metadata persistence fails after
  profile apply.

Not implemented yet:
  --json, VMess/Trojan/Shadowsocks share URI import, non-Base64 subscription
  formats, subscription delete, scheduled updates
`)
}
