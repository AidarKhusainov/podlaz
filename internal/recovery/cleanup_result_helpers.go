package recovery

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func recoveredWithMessage(candidate Candidate, message string) CleanupResult {
	return CleanupResult{Candidate: candidate, Status: "recovered", Message: message}
}

func removeEmptyGeneratedRoot(runtimeDir string) error {
	root := filepath.Join(filepath.Clean(runtimeDir), generatedDirName)
	entries, err := os.ReadDir(root)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("inspect generated runtime config directory %s: %w", root, err)
	case len(entries) != 0:
		return nil
	}
	if err := os.Remove(root); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		// A concurrent writer may have made the directory non-empty between the
		// read and remove. Preserve it rather than treating that race as cleanup
		// authority over the new file.
		entries, inspectErr := os.ReadDir(root)
		if inspectErr == nil && len(entries) != 0 {
			return nil
		}
		return fmt.Errorf("remove empty generated runtime config directory %s: %w", root, err)
	}
	return nil
}
