package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/profile"
)

func TestCompletionProfileSurfaceIsSmallAndHumanOriented(t *testing.T) {
	dir := t.TempDir()
	opts := options{profileStorePath: filepath.Join(dir, "profiles.json")}
	storeCompletionProfile(t, opts, "russia-1", "Russia 1")

	commands := completepodlaz(completionRequest{Shell: "bash", Cursor: 2, Words: []string{"podlaz", "profile", ""}}, opts)
	for _, want := range []string{"list", "show", "use", "delete"} {
		assertCompletionCandidate(t, commands, want)
	}
	for _, forbidden := range []string{"add", "import", "validate"} {
		assertNoCompletionCandidate(t, commands, forbidden)
	}

	profiles := completepodlaz(completionRequest{Shell: "zsh", Cursor: 2, Words: []string{"podlaz", "connect", ""}}, opts)
	assertCompletionCandidate(t, profiles, "Russia 1")
	assertNoCompletionCandidate(t, profiles, "russia-1")

	flags := completepodlaz(completionRequest{Shell: "fish", Cursor: 2, Words: []string{"podlaz", "connect", "--"}}, opts)
	if len(flags.Candidates) != 0 {
		t.Fatalf("canonical connect exposed lifecycle flags: %#v", flags.Candidates)
	}
}

func TestCompletionProfileListOffersExplicitIDsFlag(t *testing.T) {
	flags := completepodlaz(completionRequest{Shell: "bash", Cursor: 3, Words: []string{"podlaz", "profile", "list", "--"}}, options{})
	assertCompletionCandidate(t, flags, "--ids")
	assertNoCompletionCandidate(t, flags, "--json")
}

func TestCompletionDebugOwnsAdvancedSurface(t *testing.T) {
	debug := completepodlaz(completionRequest{Shell: "bash", Cursor: 2, Words: []string{"podlaz", "debug", ""}}, options{})
	for _, want := range []string{"doctor", "logs", "proxy", "recover"} {
		assertCompletionCandidate(t, debug, want)
	}

	doctorFlags := completepodlaz(completionRequest{Shell: "bash", Cursor: 3, Words: []string{"podlaz", "debug", "doctor", "--"}}, options{})
	for _, want := range []string{"--tun", "--verbose", "--json"} {
		assertCompletionCandidate(t, doctorFlags, want)
	}

	recoverFlags := completepodlaz(completionRequest{Shell: "bash", Cursor: 3, Words: []string{"podlaz", "debug", "recover", "--"}}, options{})
	for _, want := range []string{"--execute", "--json"} {
		assertCompletionCandidate(t, recoverFlags, want)
	}
	assertNoCompletionCandidate(t, recoverFlags, "--yes")
}

func TestCompletionSubscriptionDeleteKeepsOnlyDeletionSpecificFlags(t *testing.T) {
	flags := completepodlaz(completionRequest{Shell: "fish", Cursor: 4, Words: []string{"podlaz", "subscription", "delete", "personal", "--"}}, options{})
	assertCompletionCandidate(t, flags, "--yes")
	assertCompletionCandidate(t, flags, "--keep-profiles")
	assertNoCompletionCandidate(t, flags, "--json")
}

func TestCompletionAmbiguousProfileNamesFallBackToStableIDs(t *testing.T) {
	dir := t.TempDir()
	opts := options{profileStorePath: filepath.Join(dir, "profiles.json")}
	store, err := profile.NewStore(opts.profileStorePath)
	if err != nil {
		t.Fatal(err)
	}
	first := testConnectProfile()
	first.ID, first.Name = "work-a", "Work"
	second := testConnectProfile()
	second.ID, second.Name = "work-b", " work "
	if err := store.Add(first); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(second); err != nil {
		t.Fatal(err)
	}

	got := completepodlaz(completionRequest{Shell: "bash", Cursor: 2, Words: []string{"podlaz", "connect", ""}}, opts)
	assertCompletionCandidate(t, got, "work-a")
	assertCompletionCandidate(t, got, "work-b")
	assertNoCompletionCandidate(t, got, "Work")
	for _, candidate := range got.Candidates {
		if candidate.Value == "work-a" && !strings.Contains(candidate.Description, "stable ID") {
			t.Fatalf("ambiguous selector description = %q", candidate.Description)
		}
	}
}

func storeCompletionProfile(t *testing.T, opts options, id, name string) string {
	t.Helper()
	store, err := profile.NewStore(opts.profileStorePath)
	if err != nil {
		t.Fatal(err)
	}
	p := testConnectProfile()
	p.ID, p.Name = id, name
	if err := store.Add(p); err != nil {
		t.Fatal(err)
	}
	return p.ID
}

func assertCompletionCandidate(t *testing.T, result completionResult, want string) {
	t.Helper()
	for _, candidate := range result.Candidates {
		if candidate.Value == want {
			return
		}
	}
	t.Fatalf("expected candidate %q, got %#v", want, result.Candidates)
}

func assertNoCompletionCandidate(t *testing.T, result completionResult, want string) {
	t.Helper()
	for _, candidate := range result.Candidates {
		if candidate.Value == want {
			t.Fatalf("unexpected candidate %q in %#v", want, result.Candidates)
		}
	}
}
