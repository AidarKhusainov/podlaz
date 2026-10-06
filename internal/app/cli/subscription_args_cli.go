package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/sub"
)

func parseSubscriptionShowArgs(args []string) (string, error) {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") || strings.TrimSpace(args[0]) == "" {
		return "", usageError("subscription show requires exactly one subscription id")
	}
	return args[0], nil
}

func parseSubscriptionUpdateArgs(args []string) (string, error) {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") || strings.TrimSpace(args[0]) == "" {
		return "", usageError("subscription update requires exactly one subscription id")
	}
	return args[0], nil
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
