package daemon

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestNetworkSessionLifecycleFailedReplacementRestoresPreviousContinuation(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	previous := testContinuationRequest()
	if err := store.Save(previous); err != nil {
		t.Fatalf("save previous continuation: %v", err)
	}

	replacement := previous
	replacement.Profile.ID = "profile-replacement"
	replacement.Profile.Name = "Replacement profile"
	replacement.Profile.Server = "replacement.example.test"
	replacement.Handoff = api.HandoffReplacePodlaz
	events := []string{}
	lifecycle := newNetworkSessionLifecycle(networkSessionRecordingLifecycle{
		events:     &events,
		connectErr: errors.New("replacement rejected"),
	}, store)

	if _, err := lifecycle.Connect(context.Background(), replacement); err == nil {
		t.Fatal("expected replacement failure")
	}
	got, ok, err := store.LoadCurrent()
	if err != nil {
		t.Fatalf("load restored continuation: %v", err)
	}
	if !ok {
		t.Fatal("failed replacement must retain previous active session continuation")
	}
	if !reflect.DeepEqual(got, previous) {
		t.Fatalf("restored continuation = %#v, want %#v", got, previous)
	}
}
