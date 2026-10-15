package daemon

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestProtectedSessionRejectsUnsafeProfileChangeAsConflictWithoutMutation(t *testing.T) {
	store := newNetworkSessionStateStore(t.TempDir(), fixedBootID("boot-a"))
	original := testContinuationRequest()
	before, err := store.BeginOrResume(original)
	if err != nil {
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
	changed.Profile.ID = "another-profile"
	changed.Profile.Name = "Another profile"
	changed.Profile.Server = "other.example.test"
	_, err = store.BeginOrResume(changed)
	if err == nil {
		t.Fatal("expected blocked replacement without verified protected handoff")
	}
	// The Network Session store error is wrapped by the production continuation
	// path before HTTP mapping. It must still be a conflict, not an HTTP 500.
	if code := daemonAPIHTTPStatusCode(fmt.Errorf("persist network session continuation: %w", err)); code != http.StatusConflict {
		t.Fatalf("HTTP status = %d, want 409: %v", code, err)
	}
	if !strings.Contains(err.Error(), "podlaz debug doctor --tun") {
		t.Fatalf("protected-session conflict must have safe user guidance: %v", err)
	}

	after, exists, err := store.Load()
	if err != nil || !exists {
		t.Fatalf("reload blocked session: exists=%v err=%v", exists, err)
	}
	if after.SessionID != before.SessionID || after.Request.Profile.ID != original.Profile.ID || after.Replacement != nil ||
		after.Protection == nil || after.Protection.State != networkSessionProtectionArmed {
		t.Fatalf("blocked request modified protected session authority: %#v", after)
	}
}
