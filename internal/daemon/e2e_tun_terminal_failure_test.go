package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestE2ETunTerminalFailureTriggerSchedulesSourceResync(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(e2eTunTerminalFailureEnv, "true")
	t.Setenv(e2eTunTerminalFailureDirEnv, dir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	triggers := make(chan tunRevalidationTrigger, 1)
	startE2ETunTerminalFailureTrigger(ctx, func(trigger tunRevalidationTrigger) {
		triggers <- trigger
	})

	marker := filepath.Join(dir, e2eTunTerminalFailureMarker)
	if err := os.WriteFile(marker, []byte("trigger\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	select {
	case trigger := <-triggers:
		if trigger != tunRevalidationTriggerSourceResync {
			t.Fatalf("trigger=%q, want source-resync", trigger)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("terminal failure marker did not schedule revalidation")
	}
}

func TestE2ETunTerminalFailureVerifierConsumesMarkerAsOwnershipInvalid(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(e2eTunTerminalFailureEnv, "1")
	t.Setenv(e2eTunTerminalFailureDirEnv, dir)
	marker := filepath.Join(dir, e2eTunTerminalFailureMarker)
	if err := os.WriteFile(marker, []byte("trigger\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := maybeInjectE2ETunTerminalFailure()
	if err == nil {
		t.Fatal("enabled terminal failure marker must inject a verification failure")
	}
	var verificationErr *tunRevalidationVerificationError
	if !errors.As(err, &verificationErr) {
		t.Fatalf("injected error type=%T, want tunRevalidationVerificationError", err)
	}
	if verificationErr.classification != api.TunHealthOwnershipInvalid {
		t.Fatalf("classification=%q, want %q", verificationErr.classification, api.TunHealthOwnershipInvalid)
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("terminal failure marker must be consumed once, stat err=%v", statErr)
	}
}

func TestE2ETunTerminalFailureDisabledDoesNotInject(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(e2eTunTerminalFailureEnv, "false")
	t.Setenv(e2eTunTerminalFailureDirEnv, dir)
	marker := filepath.Join(dir, e2eTunTerminalFailureMarker)
	if err := os.WriteFile(marker, []byte("trigger\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := maybeInjectE2ETunTerminalFailure(); err != nil {
		t.Fatalf("disabled terminal failure hook injected error: %v", err)
	}
}
