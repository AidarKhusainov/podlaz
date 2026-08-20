package daemon

import (
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestNetworkSessionProtectedReplaceTreatsHandoffAsOneShotTransition(t *testing.T) {
	store := newNetworkSessionStateStore(t.TempDir(), fixedBootID("boot-a"))
	original := testContinuationRequest()
	state, err := store.BeginOrResume(original)
	if err != nil {
		t.Fatalf("begin original session: %v", err)
	}
	protection := networkSessionProtection{
		State:              networkSessionProtectionArmed,
		CompositionVersion: privacyEnvelopeCompositionVersion,
		Family:             privacyEnvelopeFamily,
		Table:              "podlaz_pe_001122334455",
		TunInterface:       "podlaz0",
		BootstrapIPv4:      []string{"192.0.2.10"},
	}
	if err := store.SetProtection(&protection); err != nil {
		t.Fatalf("protect original session: %v", err)
	}

	replacement := original
	replacement.Handoff = api.HandoffReplacePodlaz
	replaced, err := store.BeginOrResume(replacement)
	if err != nil {
		t.Fatalf("same-session protected replace must be admitted: %v", err)
	}
	if replaced.SessionID != state.SessionID {
		t.Fatalf("replace changed Network Session identity: got %q want %q", replaced.SessionID, state.SessionID)
	}
	if replaced.Replacement == nil {
		t.Fatal("protected replace must persist rollback authority before generation mutation")
	}
	if replaced.Replacement.PreviousRequest.Handoff != original.Handoff {
		t.Fatalf("previous request handoff lost: %#v", replaced.Replacement.PreviousRequest)
	}
	if replaced.Replacement.PreviousProtection == nil || replaced.Replacement.PreviousProtection.BootstrapIPv4[0] != "192.0.2.10" {
		t.Fatalf("previous protection authority lost: %#v", replaced.Replacement.PreviousProtection)
	}
	if replaced.Request.Handoff != api.HandoffReplacePodlaz {
		t.Fatalf("current replacement request lost one-shot handoff: %#v", replaced.Request)
	}
}

func TestNetworkSessionProtectedReplaceCanChangeProfileAndEndpointOnlyWithExplicitReplace(t *testing.T) {
	store := newNetworkSessionStateStore(t.TempDir(), fixedBootID("boot-a"))
	original := testContinuationRequest()
	if _, err := store.BeginOrResume(original); err != nil {
		t.Fatal(err)
	}
	protection := networkSessionProtection{
		State:              networkSessionProtectionArmed,
		CompositionVersion: privacyEnvelopeCompositionVersion,
		Family:             privacyEnvelopeFamily,
		Table:              "podlaz_pe_001122334455",
		TunInterface:       "podlaz0",
		BootstrapIPv4:      []string{"192.0.2.10"},
	}
	if err := store.SetProtection(&protection); err != nil {
		t.Fatal(err)
	}

	changed := original
	changed.Profile.ID = "profile-replacement"
	changed.Profile.Name = "Replacement profile"
	changed.Profile.Server = "replacement.example.test"
	if _, err := store.BeginOrResume(changed); err == nil {
		t.Fatal("material protected replacement without replace-podlaz must be rejected")
	}

	changed.Handoff = api.HandoffReplacePodlaz
	state, err := store.BeginOrResume(changed)
	if err != nil {
		t.Fatalf("explicit protected replacement with changed endpoint: %v", err)
	}
	if state.Replacement == nil || state.Replacement.PreviousRequest.Profile.ID != original.Profile.ID {
		t.Fatalf("changed endpoint replacement lost previous generation authority: %#v", state.Replacement)
	}
}

func TestNetworkSessionReplacementCommitMakesHandoffNonReplayable(t *testing.T) {
	store := newNetworkSessionStateStore(t.TempDir(), fixedBootID("boot-a"))
	original := testContinuationRequest()
	if _, err := store.BeginOrResume(original); err != nil {
		t.Fatal(err)
	}
	protection := networkSessionProtection{
		State:              networkSessionProtectionArmed,
		CompositionVersion: privacyEnvelopeCompositionVersion,
		Family:             privacyEnvelopeFamily,
		Table:              "podlaz_pe_001122334455",
		TunInterface:       "podlaz0",
		BootstrapIPv4:      []string{"192.0.2.20"},
	}
	if err := store.SetProtection(&protection); err != nil {
		t.Fatal(err)
	}
	replacement := original
	replacement.Handoff = api.HandoffReplacePodlaz
	if _, err := store.BeginOrResume(replacement); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitReplacement(); err != nil {
		t.Fatalf("commit replacement: %v", err)
	}
	state, exists, err := store.Load()
	if err != nil || !exists {
		t.Fatalf("load committed replacement: exists=%v err=%v", exists, err)
	}
	if state.Replacement != nil {
		t.Fatalf("committed replacement retained transition metadata: %#v", state.Replacement)
	}
	if api.NormalizeHandoffPolicy(state.Request.Handoff) != api.HandoffBlock {
		t.Fatalf("committed replacement would replay handoff after restart: %q", state.Request.Handoff)
	}
}
