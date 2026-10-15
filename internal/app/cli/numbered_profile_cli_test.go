package cli

import (
    "bytes"
    "context"
    "errors"
    "io"
    "path/filepath"
    "strings"
    "testing"

    "github.com/AidarKhusainov/podlaz/internal/api"
    "github.com/AidarKhusainov/podlaz/internal/network/planner"
    "github.com/AidarKhusainov/podlaz/internal/profile"
    "github.com/AidarKhusainov/podlaz/internal/status"
)

func TestNumberedListRoundTripsEveryProfileCommand(t *testing.T) {
    storePath := filepath.Join(t.TempDir(), "profiles.json")
    opts := options{profileStorePath: storePath}
    later := addTestProfile(t, opts, "zulu", "Второй")
    earlier := addTestProfile(t, opts, "alpha", "Второй")
    store, _ := profile.NewStore(storePath)
    if _, err := store.Select(earlier.ID); err != nil { t.Fatal(err) }

    for _, detailed := range []bool{false, true} {
        args := []string{"profile", "list"}
        if detailed { args = append(args, "--ids") }
        var out bytes.Buffer
        if err := runWithOptions(context.Background(), args, &out, opts); err != nil { t.Fatal(err) }
        lines := strings.Split(strings.TrimSpace(out.String()), "\n")
        if len(lines) != 3 { t.Fatalf("list lines=%q", out.String()) }
        if !strings.HasPrefix(strings.TrimSpace(lines[0]), "#") ||
            !strings.HasPrefix(strings.TrimSpace(lines[1]), "1") ||
            !strings.HasPrefix(strings.TrimSpace(lines[2]), "2") {
            t.Fatalf("unnumbered list: %q", out.String())
        }
        if !strings.Contains(lines[1], "*") || strings.Contains(lines[2], "*") {
            t.Fatalf("selected marker disagrees with row: %q", out.String())
        }
        if detailed && (!strings.Contains(lines[1], earlier.ID) || !strings.Contains(lines[2], later.ID)) {
            t.Fatalf("IDs disagree with rows: %q", out.String())
        }
        if !detailed && (strings.Contains(out.String(), earlier.ID) || strings.Contains(out.String(), later.ID)) {
            t.Fatalf("default list leaked stable IDs: %q", out.String())
        }
    }
    var shown bytes.Buffer
    if err := runWithOptions(context.Background(), []string{"profile", "show", "2"}, &shown, opts); err != nil { t.Fatal(err) }
    if !strings.Contains(shown.String(), "ID: "+later.ID) { t.Fatalf("show 2=%q", shown.String()) }

    var connected api.ConnectRequest
    common := options{
        profileStorePath: storePath,
        daemonStatus: func(context.Context) (status.Report, error) { return status.Report{Connection:"inactive"}, nil },
        connect: func(_ context.Context, req api.ConnectRequest) (api.LifecycleResponse, error) {
            connected = req
            return api.LifecycleResponse{Connection:"active", Mode:req.Mode}, nil
        },
    }
    if err := runWithOptions(context.Background(), []string{"connect", "2"}, &bytes.Buffer{}, common); err != nil { t.Fatal(err) }
    if connected.Profile.ID != later.ID || connected.Mode != planner.ModeTun { t.Fatalf("connected=%+v", connected) }
    if id, _ := store.SelectedID(); id != earlier.ID { t.Fatalf("connect changed selection=%q", id) }

    var boot api.AutostartConfigureRequest
    common.autostartEnable = func(_ context.Context, req api.AutostartConfigureRequest) (api.AutostartStatusResponse, error) {
        boot = req
        return api.AutostartStatusResponse{Enabled:true, Mode:req.Mode}, nil
    }
    if err := runWithOptions(context.Background(), []string{"autostart", "enable", "2"}, &bytes.Buffer{}, common); err != nil { t.Fatal(err) }
    if boot.Profile.ID != later.ID { t.Fatalf("autostart bound=%q", boot.Profile.ID) }
    if id, _ := store.SelectedID(); id != earlier.ID { t.Fatalf("autostart changed selection=%q", id) }

    if err := runWithOptions(context.Background(), []string{"debug", "proxy", "2"}, &bytes.Buffer{}, common); err != nil { t.Fatal(err) }
    if connected.Profile.ID != later.ID || connected.Mode != planner.ModeProxyOnly { t.Fatalf("proxy=%+v", connected) }

    if err := runWithOptions(context.Background(), []string{"profile", "use", "2"}, &bytes.Buffer{}, opts); err != nil { t.Fatal(err) }
    if id, _ := store.SelectedID(); id != later.ID { t.Fatalf("persisted index instead of ID: %q", id) }
}

func TestNumericNamesAndIDsNeverOverrideListRow(t *testing.T) {
    opts := options{profileStorePath: filepath.Join(t.TempDir(), "profiles.json")}
    a := addTestProfile(t, opts, "a", "2")
    b := addTestProfile(t, opts, "b", "Два")
    var row bytes.Buffer
    if err := runWithOptions(context.Background(), []string{"profile","show","2"}, &row, opts); err != nil { t.Fatal(err) }
    if !strings.Contains(row.String(), "ID: "+b.ID) { t.Fatalf("row selector chose name: %q", row.String()) }
    row.Reset()
    if err := runWithOptions(context.Background(), []string{"profile","show",a.ID}, &row, opts); err != nil { t.Fatal(err) }
    if !strings.Contains(row.String(), "ID: "+a.ID) { t.Fatalf("stable selector=%q", row.String()) }

    numeric := addTestProfile(t, opts, "42", "Числовой ID")
    row.Reset()
    if err := runWithOptions(context.Background(), []string{"profile","show","id:42"}, &row, opts); err != nil { t.Fatal(err) }
    if !strings.Contains(row.String(), "ID: "+numeric.ID) { t.Fatalf("explicit numeric ID=%q", row.String()) }
}

func TestNumberedDeleteDefaultsNoAndRejectsAutomation(t *testing.T) {
    storePath := filepath.Join(t.TempDir(), "profiles.json")
    opts := options{profileStorePath: storePath, stdinIsTerminal:func()bool{return true}, stdin:strings.NewReader("\n")}
    first := addTestProfile(t, opts, "a", "Первый")
    second := addTestProfile(t, opts, "b", "Второй")
    var prompt bytes.Buffer
    err := runWithOptions(context.Background(), []string{"profile","delete","2"}, &prompt, opts)
    if err == nil || ExitCode(err) != 1 || !strings.Contains(prompt.String(), "Delete profile Второй? [y/N]:") {
        t.Fatalf("default No err=%v prompt=%q", err, prompt.String())
    }
    for _, terminal := range []bool{false,true} {
        scripts := options{profileStorePath:storePath, stdinIsTerminal:func()bool{return terminal}}
        for _, selector := range []string{"2", "0", "999999999999999999999999999"} {
            err := runWithOptions(context.Background(), []string{"profile","delete",selector,"--yes"}, &bytes.Buffer{}, scripts)
            if err == nil || ExitCode(err) != 2 { t.Fatalf("unsafe automation selector=%q terminal=%t err=%v", selector,terminal,err) }
        }
    }
    store, _ := profile.NewStore(storePath)
    if _, err := store.Get(first.ID); err != nil { t.Fatal(err) }
    if _, err := store.Get(second.ID); err != nil { t.Fatal(err) }

    opts.stdin = strings.NewReader("yes\n")
    prompt.Reset()
    if err := runWithOptions(context.Background(), []string{"profile","delete","2"}, &prompt, opts); err != nil { t.Fatal(err) }
    if !strings.Contains(prompt.String(), "Profile deleted: Второй") { t.Fatalf("delete result=%q", prompt.String()) }
    if _, err := store.Get(second.ID); !errors.Is(err, profile.ErrNotFound) { t.Fatalf("row 2 not removed: %v", err) }
}

type mutationOnRead struct {
    once bool
    apply func()
}

func (m *mutationOnRead) Read(p []byte) (int, error) {
    if !m.once { m.once=true; m.apply() }
    n := copy(p, "yes\n")
    if n == 0 { return 0, io.EOF }
    return n, nil
}

func TestNumberedDeleteAbortsWhenListChangesDuringPrompt(t *testing.T) {
    for _, scenario := range []string{"rename", "reorder", "removed"} {
        t.Run(scenario, func(t *testing.T) {
            storePath := filepath.Join(t.TempDir(), "profiles.json")
            opts := options{profileStorePath:storePath, stdinIsTerminal:func()bool{return true}}
            first := addTestProfile(t, opts, "b", "Первый")
            second := addTestProfile(t, opts, "c", "Второй")
            store, _ := profile.NewStore(storePath)
            opts.stdin = &mutationOnRead{apply:func() {
                switch scenario {
                case "rename":
                    newProfile := second
                    newProfile.Name = "Changed"
                    if err := store.Delete(second.ID); err != nil { t.Fatal(err) }
                    if err := store.Add(newProfile); err != nil { t.Fatal(err) }
                case "reorder":
                    addTestProfile(t, opts, "a", "Added")
                case "removed":
                    if err := store.Delete(second.ID); err != nil { t.Fatal(err) }
                }
            }}
            var out bytes.Buffer
            err := runWithOptions(context.Background(), []string{"profile","delete","2"}, &out, opts)
            if !errors.Is(err, profile.ErrProfileChanged) {
                t.Fatalf("stale confirmation removed profile: err=%v, out=%q", err, out.String())
            }
            if !strings.Contains(out.String(), "Delete profile Второй? [y/N]:") {
                t.Fatalf("confirmation did not show target: %q", out.String())
            }
            if _, err := store.Get(first.ID); err != nil { t.Fatalf("removed other profile: %v", err) }
            if scenario != "removed" {
                if _, err := store.Get(second.ID); err != nil { t.Fatalf("removed unconfirmed identity: %v", err) }
            }
        })
    }
}
