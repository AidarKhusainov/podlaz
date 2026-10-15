package logs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type followObservedWriter struct {
	bytes.Buffer
	once     sync.Once
	observed chan struct{}
}

func (w *followObservedWriter) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	if bytes.Contains(p[:n], []byte("follow line")) {
		w.once.Do(func() { close(w.observed) })
	}
	return n, err
}

// Wait until the test child is actually streaming before cancelling it.
func TestRunJournalctlFollowStopsOnContextCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake journalctl script uses POSIX shell")
	}

	dir := t.TempDir()
	argsFile := filepath.Join(dir, "journalctl.args")
	journalctl := filepath.Join(dir, "journalctl")
	script := "#!/usr/bin/env bash\n" +
		"printf '%s\\n' \"$@\" >\"${JOURNALCTL_ARGS_FILE}\"\n" +
		"printf 'podlazd.service: follow line\\n'\n" +
		"exec sleep 30\n"
	if err := os.WriteFile(journalctl, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake journalctl: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("JOURNALCTL_ARGS_FILE", argsFile)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &followObservedWriter{observed: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- RunJournalctl(ctx, out, Options{Follow: true}) }()

	select {
	case <-out.observed:
	case <-time.After(5 * time.Second):
		t.Fatal("fake journalctl did not produce an initial line")
	}
	cancel()
	started := time.Now()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected context cancellation error")
		}
		if elapsed := time.Since(started); elapsed > 2*time.Second {
			t.Fatalf("follow command did not stop promptly; elapsed=%s err=%v", elapsed, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("follow command did not stop after cancellation")
	}
	if !strings.Contains(out.String(), "follow line") {
		t.Fatalf("expected fake journalctl output before cancellation, got %q", out.String())
	}
	args, readErr := os.ReadFile(argsFile)
	if readErr != nil {
		t.Fatalf("read fake journalctl args: %v", readErr)
	}
	if !strings.Contains(string(args), "--follow") {
		t.Fatalf("expected journalctl args to include --follow, got %q", string(args))
	}
}
