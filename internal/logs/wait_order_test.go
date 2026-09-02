package logs

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"testing"
)

func TestJournalReadersDrainBeforeWait(t *testing.T) {
	t.Run("core filtering drains same backend without daemon output writes", func(t *testing.T) {
		cmd := newJournalDrainCommand("Aug 17 host podlazd[123]: daemon lifecycle line\n")
		var out bytes.Buffer

		count, err := runJournalctlCommand(cmd, func() {}, &out, true)
		if err != nil {
			t.Fatalf("core journal command: %v", err)
		}
		if count != 0 || out.Len() != 0 {
			t.Fatalf("core filtered output count=%d output=%q", count, out.String())
		}
		select {
		case <-cmd.waitCalled:
		default:
			t.Fatal("core command did not wait after draining the filtered stream")
		}
	})

	t.Run("daemon output must finish draining before wait", func(t *testing.T) {
		cmd := newJournalDrainCommand("Aug 17 host podlazd[123]: daemon lifecycle line\n")
		dst := &gateWriter{
			started: make(chan struct{}),
			release: make(chan struct{}),
		}
		done := make(chan error, 1)
		go func() {
			_, err := runJournalctlCommand(cmd, func() {}, dst, false)
			done <- err
		}()

		<-dst.started
		select {
		case <-cmd.waitCalled:
			t.Fatal("journalctl Wait ran before daemon stdout finished draining")
		default:
		}

		close(dst.release)
		if err := <-done; err != nil {
			t.Fatalf("daemon journal command: %v", err)
		}
		select {
		case <-cmd.waitCalled:
		default:
			t.Fatal("journalctl Wait was not called after daemon stdout drained")
		}
	})
}

type journalDrainCommand struct {
	stdoutReader *io.PipeReader
	stdoutWriter *io.PipeWriter
	stderrReader *io.PipeReader
	stderrWriter *io.PipeWriter
	stdout       string
	waitCalled   chan struct{}
	waitOnce     sync.Once
}

func newJournalDrainCommand(stdout string) *journalDrainCommand {
	stdoutReader, stdoutWriter := io.Pipe()
	stderrReader, stderrWriter := io.Pipe()
	return &journalDrainCommand{
		stdoutReader: stdoutReader,
		stdoutWriter: stdoutWriter,
		stderrReader: stderrReader,
		stderrWriter: stderrWriter,
		stdout:       stdout,
		waitCalled:   make(chan struct{}),
	}
}

func (c *journalDrainCommand) StdoutPipe() (io.ReadCloser, error) { return c.stdoutReader, nil }
func (c *journalDrainCommand) StderrPipe() (io.ReadCloser, error) { return c.stderrReader, nil }

func (c *journalDrainCommand) Start() error {
	go func() {
		_, _ = fmt.Fprint(c.stdoutWriter, c.stdout)
		_ = c.stdoutWriter.Close()
	}()
	go func() { _ = c.stderrWriter.Close() }()
	return nil
}

func (c *journalDrainCommand) Wait() error {
	c.waitOnce.Do(func() { close(c.waitCalled) })
	return nil
}

type gateWriter struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *gateWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	<-w.release
	return len(p), nil
}
