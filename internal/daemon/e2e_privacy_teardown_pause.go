package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	e2ePrivacyTeardownPauseEnv        = "PODLAZ_E2E_PRIVACY_TEARDOWN_PAUSE"
	e2ePrivacyTeardownPauseDirEnv     = "PODLAZ_E2E_PRIVACY_TEARDOWN_PAUSE_DIR"
	e2ePrivacyTeardownPauseTimeoutEnv = "PODLAZ_E2E_PRIVACY_TEARDOWN_PAUSE_TIMEOUT_SECONDS"
	e2ePrivacyTeardownReadyFile       = "terminal-data-plane-clean.ready"
	e2ePrivacyTeardownContinueFile    = "terminal-data-plane-clean.continue"
)

func maybePauseAfterTerminalDataPlaneCleanup(ctx context.Context) error {
	if !e2ePrivacyTeardownPauseEnabled() {
		return nil
	}
	dir := strings.TrimSpace(os.Getenv(e2ePrivacyTeardownPauseDirEnv))
	if dir == "" {
		return fmt.Errorf("%s requires %s", e2ePrivacyTeardownPauseEnv, e2ePrivacyTeardownPauseDirEnv)
	}
	dir = filepath.Clean(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create privacy teardown E2E directory: %w", err)
	}
	ready := filepath.Join(dir, e2ePrivacyTeardownReadyFile)
	if err := os.WriteFile(ready, []byte("phase=terminal-data-plane-clean\n"), 0o600); err != nil {
		return fmt.Errorf("write privacy teardown E2E ready marker: %w", err)
	}

	if ctx == nil {
		ctx = context.Background()
	}
	continuePath := filepath.Join(dir, e2ePrivacyTeardownContinueFile)
	timer := time.NewTimer(e2ePrivacyTeardownPauseTimeout())
	defer timer.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("privacy teardown E2E pause timed out")
		case <-ticker.C:
			if _, err := os.Stat(continuePath); err == nil {
				return nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("inspect privacy teardown E2E continue marker: %w", err)
			}
		}
	}
}

func e2ePrivacyTeardownPauseEnabled() bool {
	value := strings.TrimSpace(os.Getenv(e2ePrivacyTeardownPauseEnv))
	return value == "1" || strings.EqualFold(value, "true")
}

func e2ePrivacyTeardownPauseTimeout() time.Duration {
	value := strings.TrimSpace(os.Getenv(e2ePrivacyTeardownPauseTimeoutEnv))
	if value == "" {
		return 60 * time.Second
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		return 60 * time.Second
	}
	return time.Duration(seconds) * time.Second
}
