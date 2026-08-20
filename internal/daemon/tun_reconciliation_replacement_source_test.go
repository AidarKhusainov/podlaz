package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func issue262ProtectedReplacementFixture(t *testing.T, connection string, withProcess bool) (string, xrayState, *tunRuntimeProcessIdentity, networkSessionState) {
	t.Helper()
	store := seededProtectedNetworkSessionStore(t, networkSessionIntentResume)
	session, exists, err := store.Load()
	if err != nil || !exists {
		t.Fatalf("load seeded Network Session: exists=%v err=%v", exists, err)
	}

	configPath := filepath.Join(store.runtimeDir, "generated", "xray.json")
	tx := txstate.NewTransaction("tx-replacement-source", session.Request.Profile.ID, planner.ModeTun, time.Now().UTC())
	tx.State = txstate.TransactionCommitted
	tx.DesiredPlan.Core = txstate.CorePlan{
		RuntimeConfigPath: configPath,
		ProcessLabel:      "xray",
		Owner:             txstate.TransactionOwner,
	}
	tx.DesiredPlan.TUN = txstate.TUNDesiredState{InterfaceName: "podlaz0", Owner: xrayTunInboundOwner}
	var process *tunRuntimeProcessIdentity
	if withProcess {
		process = &tunRuntimeProcessIdentity{PID: 4242}
		tx.Rollback.ChildProcesses = []txstate.ChildProcessRollback{{
			PID:       process.PID,
			Label:     "xray",
			ConfigRef: configPath,
			Owner:     txstate.TransactionOwner,
		}}
	}
	if _, err := (txstate.TransactionStore{RuntimeDir: store.runtimeDir}).Save(tx); err != nil {
		t.Fatalf("save replacement source transaction: %v", err)
	}

	managerState := xrayState{
		Connection:        connection,
		Mode:              planner.ModeTun,
		ProfileID:         session.Request.Profile.ID,
		ProfileName:       session.Request.Profile.Name,
		RuntimeConfigPath: configPath,
		TransactionID:     tx.ID,
	}
	return store.runtimeDir, managerState, process, session
}

func TestProtectedReplacementSourceAcceptsActiveGeneration(t *testing.T) {
	runtimeDir, managerState, process, session := issue262ProtectedReplacementFixture(t, "active", true)

	source, err := loadProtectedTunReplacementSource(runtimeDir, managerState, process, session)
	if err != nil {
		t.Fatalf("prove active protected replacement source: %v", err)
	}
	if source.Kind != protectedTunReplacementActive {
		t.Fatalf("active source kind=%q, want %q", source.Kind, protectedTunReplacementActive)
	}
	if source.SessionID != session.SessionID || source.Transaction.ID != managerState.TransactionID {
		t.Fatalf("active source lost exact identity: %#v", source)
	}
}

func TestProtectedReplacementSourceAcceptsDegradedCoreExitedGeneration(t *testing.T) {
	runtimeDir, managerState, _, session := issue262ProtectedReplacementFixture(t, "error (core exited)", false)

	source, err := loadProtectedTunReplacementSource(runtimeDir, managerState, nil, session)
	if err != nil {
		t.Fatalf("prove degraded protected replacement source: %v", err)
	}
	if source.Kind != protectedTunReplacementDegraded {
		t.Fatalf("degraded source kind=%q, want %q", source.Kind, protectedTunReplacementDegraded)
	}
	if source.Protection.State != networkSessionProtectionArmed {
		t.Fatalf("degraded source lost armed Privacy Envelope authority: %#v", source.Protection)
	}
}

func TestProtectedReplacementSourceUsesPreviousGenerationIdentityDuringReplacement(t *testing.T) {
	runtimeDir, managerState, _, session := issue262ProtectedReplacementFixture(t, "error (core exited)", false)
	previousRequest := session.Request
	previousProtection := cloneNetworkSessionProtection(*session.Protection)
	target := session.Request
	target.Profile.ID = "profile-replacement"
	target.Profile.Name = "Replacement profile"
	target.Profile.Server = "replacement.example.test"
	target.Handoff = api.HandoffReplacePodlaz
	session.Request = target
	session.Replacement = &networkSessionReplacement{
		PreviousRequest:    previousRequest,
		PreviousProtection: &previousProtection,
	}

	source, err := loadProtectedTunReplacementSource(runtimeDir, managerState, nil, session)
	if err != nil {
		t.Fatalf("prove previous degraded generation during target replacement: %v", err)
	}
	if source.Request.Profile.ID != previousRequest.Profile.ID {
		t.Fatalf("source request profile=%q, want previous generation %q", source.Request.Profile.ID, previousRequest.Profile.ID)
	}
	if source.Transaction.ProfileID != previousRequest.Profile.ID {
		t.Fatalf("source transaction profile=%q, want previous generation %q", source.Transaction.ProfileID, previousRequest.Profile.ID)
	}
}

func TestProtectedReplacementSourceRejectsDegradedMissingTransactionAuthority(t *testing.T) {
	runtimeDir, managerState, _, session := issue262ProtectedReplacementFixture(t, "error (core exited)", false)
	managerState.TransactionID = "tx-missing-authority"

	if _, err := loadProtectedTunReplacementSource(runtimeDir, managerState, nil, session); err == nil {
		t.Fatal("degraded source without exact persisted transaction authority was accepted")
	}
}

func TestProtectedReplacementSourceRejectsDifferentNetworkSession(t *testing.T) {
	runtimeDir, managerState, _, session := issue262ProtectedReplacementFixture(t, "error (core exited)", false)
	session.Request.Profile.ID = "profile-other-session"

	if _, err := loadProtectedTunReplacementSource(runtimeDir, managerState, nil, session); err == nil {
		t.Fatal("transaction/manager identity from another Network Session was accepted")
	}
}

func TestProtectedReplacementSourceRejectsLiveProcessOnDegradedState(t *testing.T) {
	runtimeDir, managerState, _, session := issue262ProtectedReplacementFixture(t, "error (core exited)", false)
	process := &tunRuntimeProcessIdentity{PID: 9999}

	if _, err := loadProtectedTunReplacementSource(runtimeDir, managerState, process, session); err == nil {
		t.Fatal("degraded source with a live process identity was accepted")
	}
}
