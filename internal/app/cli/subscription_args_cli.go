package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/sub"
)

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
