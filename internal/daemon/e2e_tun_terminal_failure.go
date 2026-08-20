package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

const (
	e2eTunTerminalFailureEnv        = "PODLAZ_E2E_TUN_TERMINAL_FAILURE"
	e2eTunTerminalFailureDirEnv     = "PODLAZ_E2E_TUN_TERMINAL_FAILURE_DIR"
	e2eTunTerminalFailureMarker     = "terminal-failure.trigger"
	e2eTunTerminalFailurePollPeriod = 25 * time.Millisecond
)

// startE2ETunTerminalFailureTrigger is a narrow installed-package acceptance
// hook. When explicitly enabled, one marker schedules a normal source-resync
// through the existing revalidation coordinator. The verifier consumes the same
// marker and returns the same ownership-invalid classification as a real
// unrecoverable owned-state verification failure; terminal policy and teardown
// are otherwise the production path.
func startE2ETunTerminalFailureTrigger(ctx context.Context, notify tunNetworkEventNotifyFunc) {
	if !e2eTunTerminalFailureEnabled() || notify == nil {
		return
	}
	marker, err := e2eTunTerminalFailureMarkerPath()
	if err != nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		ticker := time.NewTicker(e2eTunTerminalFailurePollPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, statErr := os.Stat(marker); statErr == nil {
					notify(tunRevalidationTriggerSourceResync)
					return
				} else if !errors.Is(statErr, os.ErrNotExist) {
					return
				}
			}
		}
	}()
}

func maybeInjectE2ETunTerminalFailure() error {
	if !e2eTunTerminalFailureEnabled() {
		return nil
	}
	marker, err := e2eTunTerminalFailureMarkerPath()
	if err != nil {
		return newTunRevalidationVerificationError(api.TunHealthOwnershipInvalid, err)
	}
	if _, err := os.Stat(marker); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return newTunRevalidationVerificationError(api.TunHealthOwnershipInvalid, fmt.Errorf("inspect terminal failure E2E marker: %w", err))
	}
	if err := os.Remove(marker); err != nil {
		return newTunRevalidationVerificationError(api.TunHealthOwnershipInvalid, fmt.Errorf("consume terminal failure E2E marker: %w", err))
	}
	return newTunRevalidationVerificationError(api.TunHealthOwnershipInvalid, errors.New("controlled E2E terminal TUN ownership verification failure"))
}

func e2eTunTerminalFailureEnabled() bool {
	value := strings.TrimSpace(os.Getenv(e2eTunTerminalFailureEnv))
	return value == "1" || strings.EqualFold(value, "true")
}

func e2eTunTerminalFailureMarkerPath() (string, error) {
	dir := strings.TrimSpace(os.Getenv(e2eTunTerminalFailureDirEnv))
	if dir == "" {
		return "", fmt.Errorf("%s requires %s", e2eTunTerminalFailureEnv, e2eTunTerminalFailureDirEnv)
	}
	return filepath.Join(filepath.Clean(dir), e2eTunTerminalFailureMarker), nil
}
