package logs

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/AidarKhusainov/tunwarden/internal/render"
)

const (
	DaemonUnit   = "tunwardend.service"
	DefaultLines = "200"
)

// Options describes the read-only log stream requested by the CLI.
type Options struct {
	Follow bool
	Since  string
}

// Run prints recent tunwardend logs from journald.
func Run(ctx context.Context, stdout io.Writer, opts Options) error {
	if _, err := exec.LookPath("journalctl"); err != nil {
		return errors.New("journalctl is not available; install systemd journal tools or run on a systemd/journald host")
	}

	if _, err := fmt.Fprintln(stdout, "TunWarden daemon logs"); err != nil {
		return fmt.Errorf("write logs header: %w", err)
	}
	return RunJournalctl(ctx, stdout, opts)
}

// BuildJournalctlArgs returns the exact journalctl argument vector for daemon logs.
func BuildJournalctlArgs(opts Options) []string {
	args := []string{
		"--system",
		"--unit", DaemonUnit,
		"--no-pager",
		"--output", "short",
	}
	if opts.Since != "" {
		args = append(args, "--since", opts.Since)
	} else {
		args = append(args, "--lines", DefaultLines)
	}
	if opts.Follow {
		args = append(args, "--follow")
	}
	return args
}

// RunJournalctl executes journalctl and renders redacted output lines.
func RunJournalctl(ctx context.Context, stdout io.Writer, opts Options) error {
	cmd := exec.CommandContext(ctx, "journalctl", BuildJournalctlArgs(opts)...)

	outPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("prepare journalctl stdout: %w", err)
	}
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("prepare journalctl stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start journalctl for %s: %w", DaemonUnit, err)
	}

	var stderr bytes.Buffer
	errc := make(chan scanResult, 2)
	go func() { errc <- scanResult{name: "stdout", err: scanRedacted(stdout, outPipe)} }()
	go func() { errc <- scanResult{name: "stderr", err: scanRedacted(&stderr, errPipe)} }()

	waitErr := cmd.Wait()
	for range 2 {
		result := <-errc
		if result.err != nil {
			return fmt.Errorf("read journalctl %s: %w", result.name, result.err)
		}
	}
	if waitErr != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = waitErr.Error()
		}
		return fmt.Errorf("journalctl failed for %s: %s", DaemonUnit, render.Redact(message))
	}
	return nil
}

type scanResult struct {
	name string
	err  error
}

func scanRedacted(dst io.Writer, src io.Reader) error {
	reader := bufio.NewReader(src)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimRight(line, "\r\n")
			if _, writeErr := fmt.Fprintln(dst, render.Redact(line)); writeErr != nil {
				return writeErr
			}
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
}
