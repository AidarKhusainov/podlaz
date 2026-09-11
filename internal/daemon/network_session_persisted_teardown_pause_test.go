package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPersistedTerminalConvergenceUsesDataPlaneCleanPause(t *testing.T) {
	store := seededProtectedNetworkSessionStore(t, networkSessionIntentTerminal)
	hookDir := t.TempDir()
	t.Setenv(e2ePrivacyTeardownPauseEnv, "true")
	t.Setenv(e2ePrivacyTeardownPauseDirEnv, hookDir)
	t.Setenv(e2ePrivacyTeardownPauseTimeoutEnv, "1")
	continuePath := filepath.Join(hookDir, e2ePrivacyTeardownContinueFile)
	if err := os.WriteFile(continuePath, []byte("continue\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := convergePersistedNetworkSessionTeardownWith(
		context.Background(),
		store,
		&privacyEnvelopeExecutorStub{exists: true},
		func(context.Context) error { return nil },
	); err != nil {
		t.Fatalf("converge persisted terminal teardown: %v", err)
	}
	readyPath := filepath.Join(hookDir, e2ePrivacyTeardownReadyFile)
	if _, err := os.Stat(readyPath); err != nil {
		t.Fatalf("persisted terminal convergence did not publish data-plane-clean pause: %v", err)
	}
}
