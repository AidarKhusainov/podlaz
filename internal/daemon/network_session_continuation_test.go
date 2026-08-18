package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

type recordingLifecycle struct {
	events        *[]string
	connectErr    error
	disconnectErr error
}

func (l recordingLifecycle) Connect(context.Context, api.ConnectRequest) (api.LifecycleResponse, error) {
	*l.events = append(*l.events, "connect")
	return api.LifecycleResponse{Connection: "active", Proxy: "inactive", TUN: "active"}, l.connectErr
}

func (l recordingLifecycle) Disconnect(context.Context) (api.LifecycleResponse, error) {
	*l.events = append(*l.events, "disconnect")
	return api.LifecycleResponse{Connection: "inactive", Proxy: "inactive", TUN: "disabled"}, l.disconnectErr
}

func testContinuationRequest() api.ConnectRequest {
	return api.ConnectRequest{
		Mode: "tun",
		Profile: api.ProfileSnapshot{
			ID:           "profile-example",
			Name:         "Example profile",
			Source:       "manual",
			Engine:       "xray",
			Server:       "vpn.example.test",
			Port:         443,
			Protocol:     "vless",
			UserIdentity: "00000000-0000-4000-8000-000000000001",
		},
		Handoff: api.HandoffBlock,
	}
}

func fixedBootID(value string) bootIDReader {
	return func() (string, error) { return value, nil }
}

func TestNetworkSessionContinuationIsPrivateAndCurrentBootBound(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	request := testContinuationRequest()

	if err := store.Save(request); err != nil {
		t.Fatalf("save continuation: %v", err)
	}
	info, err := os.Stat(filepath.Join(runtimeDir, networkSessionContinuationFileName))
	if err != nil {
		t.Fatalf("stat continuation: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("continuation mode = %o, want 600", got)
	}

	got, ok, err := store.LoadCurrent()
	if err != nil {
		t.Fatalf("load current continuation: %v", err)
	}
	if !ok {
		t.Fatal("expected current-boot continuation")
	}
	if !reflect.DeepEqual(got, request) {
		t.Fatalf("continuation request mismatch: got %#v want %#v", got, request)
	}
}

func TestNetworkSessionContinuationDoesNotCrossBootBoundary(t *testing.T) {
	runtimeDir := t.TempDir()
	if err := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a")).Save(testContinuationRequest()); err != nil {
		t.Fatalf("save continuation: %v", err)
	}

	store := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-b"))
	_, ok, err := store.LoadCurrent()
	if err != nil {
		t.Fatalf("load continuation after boot boundary: %v", err)
	}
	if ok {
		t.Fatal("normal connection continuation must not survive boot-id change")
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, networkSessionContinuationFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale continuation must be removed, stat error: %v", err)
	}
}

func TestNetworkSessionLifecycleArmsBeforeConnect(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	events := []string{}
	store.afterSave = func() { events = append(events, "continuation-saved") }
	inner := recordingLifecycle{events: &events}
	lifecycle := newNetworkSessionLifecycle(inner, store)

	if _, err := lifecycle.Connect(context.Background(), testContinuationRequest()); err != nil {
		t.Fatalf("connect: %v", err)
	}

	want := []string{"continuation-saved", "connect"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("event ordering = %#v, want %#v", events, want)
	}
	if _, ok, err := store.LoadCurrent(); err != nil || !ok {
		t.Fatalf("successful connect must retain continuation, ok=%v err=%v", ok, err)
	}
}

func TestNetworkSessionLifecycleReturnedConnectFailureDisarmsContinuation(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	events := []string{}
	inner := recordingLifecycle{events: &events, connectErr: errors.New("terminal connect failure")}
	lifecycle := newNetworkSessionLifecycle(inner, store)

	if _, err := lifecycle.Connect(context.Background(), testContinuationRequest()); err == nil {
		t.Fatal("expected connect failure")
	}
	if _, ok, err := store.LoadCurrent(); err != nil || ok {
		t.Fatalf("returned connect failure must disarm continuation, ok=%v err=%v", ok, err)
	}
}

func TestNetworkSessionLifecycleExplicitDisconnectDisarmsBeforeTeardown(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if err := store.Save(testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	events := []string{}
	store.afterRemove = func() { events = append(events, "continuation-removed") }
	inner := recordingLifecycle{events: &events, disconnectErr: errors.New("rollback failed")}
	lifecycle := newNetworkSessionLifecycle(inner, store)

	if _, err := lifecycle.Disconnect(context.Background()); err == nil {
		t.Fatal("expected disconnect failure")
	}
	want := []string{"continuation-removed", "disconnect"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("event ordering = %#v, want %#v", events, want)
	}
	if _, ok, err := store.LoadCurrent(); err != nil || ok {
		t.Fatalf("explicit disconnect must stay disarmed after rollback failure, ok=%v err=%v", ok, err)
	}
}

func TestNetworkSessionLifecycleRestartDisconnectPreservesContinuation(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if err := store.Save(testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	events := []string{}
	inner := recordingLifecycle{events: &events}
	lifecycle := newNetworkSessionLifecycle(inner, store)

	if _, err := lifecycle.DisconnectForRestart(context.Background()); err != nil {
		t.Fatalf("restart disconnect: %v", err)
	}
	if _, ok, err := store.LoadCurrent(); err != nil || !ok {
		t.Fatalf("restart teardown must preserve continuation, ok=%v err=%v", ok, err)
	}
}

func TestResumeNetworkSessionRecoversBeforeReconnect(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if err := store.Save(testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	events := []string{}
	lifecycle := newNetworkSessionLifecycle(recordingLifecycle{events: &events}, store)

	resumed, err := resumeNetworkSession(
		context.Background(),
		store,
		lifecycle,
		func(context.Context) api.StatusResponse { return api.StatusResponse{Connection: "inactive"} },
		func(context.Context, api.StatusResponse) api.RecoveryResponse {
			events = append(events, "recover")
			return api.RecoveryResponse{Mode: "execute"}
		},
	)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !resumed {
		t.Fatal("expected continuation to resume")
	}
	want := []string{"recover", "connect"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("startup ordering = %#v, want %#v", events, want)
	}
}

func TestResumeNetworkSessionDoesNotReconnectAfterFailedRecovery(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if err := store.Save(testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	events := []string{}
	lifecycle := newNetworkSessionLifecycle(recordingLifecycle{events: &events}, store)

	resumed, err := resumeNetworkSession(
		context.Background(),
		store,
		lifecycle,
		func(context.Context) api.StatusResponse { return api.StatusResponse{Connection: "inactive"} },
		func(context.Context, api.StatusResponse) api.RecoveryResponse {
			events = append(events, "recover")
			return api.RecoveryResponse{
				Mode: "execute",
				Results: []api.RecoveryCleanupResult{{
					Candidate: api.RecoveryCandidate{Kind: "transaction", Description: "exact transaction", Target: "tx-example"},
					Status:    "failed",
				}},
			}
		},
	)
	if !errors.Is(err, errNetworkSessionRecoveryIncomplete) {
		t.Fatalf("expected incomplete recovery error, got %v", err)
	}
	if resumed {
		t.Fatal("failed exact recovery must not reconnect")
	}
	if !reflect.DeepEqual(events, []string{"recover"}) {
		t.Fatalf("unexpected startup events: %#v", events)
	}
	if _, ok, loadErr := store.LoadCurrent(); loadErr != nil || !ok {
		t.Fatalf("recovery failure must retain continuation and exact authority, ok=%v err=%v", ok, loadErr)
	}
}
