package logs

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"testing"
)

func TestIssue254JournalReadersDrainBeforeWait(t *testing.T) {
	t.Run("core filtering drains same backend without daemon output writes", func(t *testing.T) {
		cmd := newIssue254JournalCommand("Aug 17 host podlazd[123]: daemon lifecycle line\n")
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
		cmd := newIssue254JournalCommand("Aug 17 host podlazd[123]: daemon lifecycle line\n")
		dst := &issue254GateWriter{
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

type issue254JournalCommand struct {
	stdoutReader *io.PipeReader
	stdoutWriter *io.PipeWriter
	stderrReader *io.PipeReader
	stderrWriter *io.PipeWriter
	stdout       string
	waitCalled   chan struct{}
	waitOnce     sync.Once
}

func newIssue254JournalCommand(stdout string) *issue254JournalCommand {
	stdoutReader, stdoutWriter := io.Pipe()
	stderrReader, stderrWriter := io.Pipe()
	return &issue254JournalCommand{
		stdoutReader: stdoutReader,
		stdoutWriter: stdoutWriter,
		stderrReader: stderrReader,
		stderrWriter: stderrWriter,
		stdout:       stdout,
		waitCalled:   make(chan struct{}),
	}
}

func (c *issue254JournalCommand) StdoutPipe() (io.ReadCloser, error) { return c.stdoutReader, nil }
func (c *issue254JournalCommand) StderrPipe() (io.ReadCloser, error) { return c.stderrReader, nil }

func (c *issue254JournalCommand) Start() error {
	go func() {
		_, _ = fmt.Fprint(c.stdoutWriter, c.stdout)
		_ = c.stdoutWriter.Close()
	}()
	go func() { _ = c.stderrWriter.Close() }()
	return nil
}

func (c *issue254JournalCommand) Wait() error {
	c.waitOnce.Do(func() { close(c.waitCalled) })
	return nil
}

type issue254GateWriter struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *issue254GateWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	<-w.release
	return len(p), nil
}
