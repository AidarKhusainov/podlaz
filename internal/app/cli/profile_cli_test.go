package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/profile"
)

func TestRunCLIProfileListShowUseAndDelete(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{profileStorePath: storePath}
	p := addTestProfile(t, opts, "work-profile", "Work VPN")

	var listOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "list"}, &listOut, opts); err != nil {
		t.Fatalf("profile list failed: %v", err)
	}
	gotList := listOut.String()
	for _, want := range []string{"SELECTED", "NAME", "PROTOCOL", "SOURCE", "Work VPN", "vless"} {
		if !strings.Contains(gotList, want) {
			t.Fatalf("profile list missing %q: %q", want, gotList)
		}
	}
	for _, forbidden := range []string{p.Server, p.UserIdentity, p.ID} {
		if strings.Contains(gotList, forbidden) {
			t.Fatalf("profile list leaked %q: %q", forbidden, gotList)
		}
	}

	var useOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "use", "work vpn"}, &useOut, opts); err != nil {
		t.Fatalf("profile use failed: %v", err)
	}
	if useOut.String() != "Selected profile: Work VPN\n" {
		t.Fatalf("profile use output = %q", useOut.String())
	}

	var selectedList bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "list"}, &selectedList, opts); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(selectedList.String(), "*") {
		t.Fatalf("selected profile not marked: %q", selectedList.String())
	}

	var showOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "show", "WORK VPN"}, &showOut, opts); err != nil {
		t.Fatalf("profile show failed: %v", err)
	}
	gotShow := showOut.String()
	for _, want := range []string{"Name: Work VPN", "ID: work-profile", "Protocol: vless"} {
		if !strings.Contains(gotShow, want) {
			t.Fatalf("profile show missing %q: %q", want, gotShow)
		}
	}
	if strings.Contains(gotShow, p.UserIdentity) {
		t.Fatalf("profile show leaked user identity: %q", gotShow)
	}

	var deleteOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "delete", "Work VPN", "--yes"}, &deleteOut, opts); err != nil {
		t.Fatalf("profile delete failed: %v", err)
	}
	if deleteOut.String() != "Profile deleted: Work VPN\n" {
		t.Fatalf("delete output = %q", deleteOut.String())
	}
	if _, err := profile.NewStore(storePath); err != nil {
		t.Fatal(err)
	}
}

func TestRunCLIProfileListIDsIsExplicitDisambiguationSurface(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{profileStorePath: storePath}
	addTestProfile(t, opts, "work-a", "Work")
	addTestProfile(t, opts, "work-b", " work ")

	var normal bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "list"}, &normal, opts); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"work-a", "work-b"} {
		if strings.Contains(normal.String(), forbidden) {
			t.Fatalf("normal profile list exposed stable ID %q: %q", forbidden, normal.String())
		}
	}

	var detailed bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "list", "--ids"}, &detailed, opts); err != nil {
		t.Fatalf("profile list --ids failed: %v", err)
	}
	for _, want := range []string{"ID", "work-a", "work-b", "Work"} {
		if !strings.Contains(detailed.String(), want) {
			t.Fatalf("profile list --ids missing %q: %q", want, detailed.String())
		}
	}

	err := runWithOptions(context.Background(), []string{"profile", "show", "WORK"}, &bytes.Buffer{}, opts)
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("ambiguous selector err=%v exit=%d", err, ExitCode(err))
	}
	if !strings.Contains(err.Error(), "podlaz profile list --ids") {
		t.Fatalf("ambiguous selector has no usable disambiguation path: %v", err)
	}
}

func TestRunCLIProfileUseIsOnlySelectionMutation(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{profileStorePath: storePath}
	first := addTestProfile(t, opts, "first", "First")
	addTestProfile(t, opts, "second", "Second")

	if err := runWithOptions(context.Background(), []string{"profile", "use", "First"}, &bytes.Buffer{}, opts); err != nil {
		t.Fatal(err)
	}
	store, _ := profile.NewStore(storePath)
	selected, err := store.ResolveSelected()
	if err != nil {
		t.Fatal(err)
	}
	if selected.ID != first.ID {
		t.Fatalf("selected=%q want=%q", selected.ID, first.ID)
	}

	if err := runWithOptions(context.Background(), []string{"profile", "show", "Second"}, &bytes.Buffer{}, opts); err != nil {
		t.Fatal(err)
	}
	selected, err = store.ResolveSelected()
	if err != nil || selected.ID != first.ID {
		t.Fatalf("read-only show changed selection: selected=%q err=%v", selected.ID, err)
	}
}

func TestRunCLIProfileRemovedSubcommandsAndJSONFailUsage(t *testing.T) {
	for _, args := range [][]string{
		{"profile", "add", "--name", "test"},
		{"profile", "import", "vless://example"},
		{"profile", "validate", "test"},
		{"profile", "list", "--json"},
		{"profile", "show", "test", "--json"},
	} {
		err := runWithOptions(context.Background(), args, &bytes.Buffer{}, options{profileStorePath: filepath.Join(t.TempDir(), "profiles.json")})
		if err == nil || ExitCode(err) != 2 {
			t.Fatalf("args=%v err=%v exit=%d, want usage error", args, err, ExitCode(err))
		}
	}
}

func TestRunCLIProfileDeleteRequiresYesWhenNonInteractive(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{
		profileStorePath: storePath,
		stdinIsTerminal:  func() bool { return false },
	}
	addTestProfile(t, opts, "test", "Test")

	err := runWithOptions(context.Background(), []string{"profile", "delete", "Test"}, &bytes.Buffer{}, opts)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("delete err=%v exit=%d, want usage error", err, ExitCode(err))
	}
}

func TestRunCLIProfileShowMissingAndAmbiguousSelectorsFailWithoutGuessing(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{profileStorePath: storePath}
	addTestProfile(t, opts, "work-a", "Work")
	addTestProfile(t, opts, "work-b", " work ")

	err := runWithOptions(context.Background(), []string{"profile", "show", "missing"}, &bytes.Buffer{}, opts)
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("missing selector err=%v exit=%d", err, ExitCode(err))
	}
	err = runWithOptions(context.Background(), []string{"profile", "show", "WORK"}, &bytes.Buffer{}, opts)
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous selector err=%v exit=%d", err, ExitCode(err))
	}
}

func addTestProfile(t *testing.T, opts options, id, name string) profile.Profile {
	t.Helper()
	store, err := profile.NewStore(opts.profileStorePath)
	if err != nil {
		t.Fatal(err)
	}
	p := testConnectProfile()
	p.ID = id
	p.Name = name
	if err := store.Add(p); err != nil {
		t.Fatalf("add test profile: %v", err)
	}
	return p
}
