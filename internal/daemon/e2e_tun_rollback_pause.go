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
	e2eTunRollbackPauseEnv        = "PODLAZ_E2E_TUN_ROLLBACK_PAUSE"
	e2eTunRollbackPauseDirEnv     = "PODLAZ_E2E_TUN_ROLLBACK_PAUSE_DIR"
	e2eTunRollbackPauseTimeoutEnv = "PODLAZ_E2E_TUN_ROLLBACK_PAUSE_TIMEOUT_SECONDS"
	e2eTunRollbackPauseArmFile    = "rollback-pause.arm"
	e2eTunRollbackPauseReadyFile  = "rollback-pause.ready"
	e2eTunRollbackPauseContinue   = "rollback-pause.continue"
)

func maybePauseForE2ETunRollback(ctx context.Context) error {
	if !e2eTunRollbackPauseEnabled() {
		return nil
	}
	dir := e2eTunRollbackPauseDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create E2E TUN rollback pause directory: %w", err)
	}
	arm := filepath.Join(dir, e2eTunRollbackPauseArmFile)
	if err := os.Remove(arm); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("consume E2E TUN rollback pause arm: %w", err)
	}

	ready := filepath.Join(dir, e2eTunRollbackPauseReadyFile)
	if err := os.WriteFile(ready, []byte("ready\n"), 0o600); err != nil {
		return fmt.Errorf("write E2E TUN rollback pause ready marker: %w", err)
	}
	continuePath := filepath.Join(dir, e2eTunRollbackPauseContinue)
	deadline := time.NewTimer(e2eTunRollbackPauseTimeout())
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("E2E TUN rollback pause timed out")
		case <-ticker.C:
			if _, err := os.Stat(continuePath); err == nil {
				_ = os.Remove(continuePath)
				_ = os.Remove(ready)
				return nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("inspect E2E TUN rollback pause continue marker: %w", err)
			}
		}
	}
}

func e2eTunRollbackPauseEnabled() bool {
	value := strings.TrimSpace(os.Getenv(e2eTunRollbackPauseEnv))
	return value == "1" || strings.EqualFold(value, "true")
}

func e2eTunRollbackPauseDir() string {
	value := strings.TrimSpace(os.Getenv(e2eTunRollbackPauseDirEnv))
	if value == "" {
		return filepath.Join(os.TempDir(), "podlaz-e2e-tun-rollback-pause")
	}
	return filepath.Clean(value)
}

func e2eTunRollbackPauseTimeout() time.Duration {
	value := strings.TrimSpace(os.Getenv(e2eTunRollbackPauseTimeoutEnv))
	if value == "" {
		return 60 * time.Second
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		return 60 * time.Second
	}
	return time.Duration(seconds) * time.Second
}
