package daemon

import (
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "testing"
)

func TestStartupProgressIsPrivateAndRejectsUntrustedStages(t *testing.T) {
    dir := t.TempDir()
    t.Setenv("INVOCATION_ID", strings.Repeat("a", 32))
    progress := newStartupProgressRecorder(dir)
    progress.record(startupStageSerialized)
    path := filepath.Join(dir, startupProgressFileName)
    data, err := os.ReadFile(path)
    if err != nil { t.Fatal(err) }
    expected := []string{
        "pid=" + strconv.Itoa(os.Getpid()) + "\n",
        "invocation=" + strings.Repeat("a", 32) + "\n",
        "stage=startup-serialized\n",
        "elapsed_seconds=",
    }
    for _, token := range expected {
        if !strings.Contains(string(data), token) {
            t.Fatalf("private progress missing expected token %q", token)
        }
    }
    info, err := os.Lstat(path)
    if err != nil { t.Fatal(err) }
    if info.Mode().Perm() != 0600 || !info.Mode().IsRegular() {
        t.Fatalf("progress marker must be a private regular file: %v", info.Mode())
    }
    progress.record(startupStage("api-key=secret\nendpoint=private"))
    unchanged, err := os.ReadFile(path)
    if err != nil { t.Fatal(err) }
    if string(unchanged) != string(data) {
        t.Fatal("invalid stage must never replace the private marker")
    }
    progress.record(startupStageConnectReplay)
    updated, err := os.ReadFile(path)
    if err != nil { t.Fatal(err) }
    if !strings.Contains(string(updated), "stage=connect-replay\n") {
        t.Fatal("valid stage transition was not recorded")
    }
    if strings.Contains(string(updated), "secret") {
        t.Fatal("untrusted value leaked into progress marker")
    }
}

func TestStartupProgressInvalidInvocationIsNotPersisted(t *testing.T) {
    t.Setenv("INVOCATION_ID", "credential-like-untrusted-value")
    progress := newStartupProgressRecorder(t.TempDir())
    progress.record(startupStageSerialized)
    data, err := os.ReadFile(progress.path)
    if err != nil { t.Fatal(err) }
    if !strings.Contains(string(data), "invocation=none\n") ||
        strings.Contains(string(data), "credential-like") {
        t.Fatal("startup diagnostic persisted an unvalidated invocation ID")
    }
}
