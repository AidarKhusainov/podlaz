package daemon

import (
	"bytes"
	"errors"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestNetworkSessionResumeDiagnosticStoreUsesPrivatePermissions(t *testing.T) {
	store := newNetworkSessionResumeDiagnosticStore(t.TempDir(), fixedBootID("boot-a"))
	if err := store.Save(networkSessionResumeDiagnostic{
		ResumeStage:       api.NetworkSessionResumeStageConnectReplay,
		LastResumeOutcome: api.NetworkSessionResumeOutcomeFailed,
		TUNFailurePhase:   "preflight",
		RollbackStatus:    "not-started",
	}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(store.path())
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("resume diagnostic mode=%o want=600", got)
	}
}

func TestNetworkSessionResumeFailureLogNeverIncludesNestedErrorText(t *testing.T) {
	var output bytes.Buffer
	originalOutput := log.Writer()
	originalFlags := log.Flags()
	originalPrefix := log.Prefix()
	log.SetOutput(&output)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(originalOutput)
		log.SetFlags(originalFlags)
		log.SetPrefix(originalPrefix)
	})

	secret := "private-endpoint.example.test private-token-value profile-private-value"
	err := newNetworkSessionResumeError(
		api.NetworkSessionResumeStageConnectReplay,
		false,
		withTunFailurePhase("preflight", noTunTransactionID, "not-started", errors.New(secret)),
	)
	logNetworkSessionResumeFailure(err)

	line := output.String()
	for _, want := range []string{
		"event=network_session_resume_failed",
		"resume_stage=connect-replay",
		"tun_failure_phase=preflight",
		"rollback_status=not-started",
		"transaction_present=false",
		"startup_gate=blocked",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("missing %q in resume log: %s", want, line)
		}
	}
	for _, forbidden := range strings.Fields(secret) {
		if strings.Contains(line, forbidden) {
			t.Fatalf("resume log leaked %q: %s", forbidden, line)
		}
	}
}
