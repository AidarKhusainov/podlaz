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

	"github.com/AidarKhusainov/podlaz/internal/render"
)

const (
	DaemonUnit   = "podlazd.service"
	DefaultLines = "200"
)

// Options describes the read-only log stream requested by the CLI.
type Options struct {
	Follow bool
	Since  string
	Core   bool
}

// Run prints recent podlaz logs from journald.
func Run(ctx context.Context, stdout io.Writer, opts Options) error {
	if _, err := exec.LookPath("journalctl"); err != nil {
		return errors.New("journalctl is not available; install systemd journal tools or run on a systemd/journald host")
	}

	header := "podlaz daemon logs"
	if opts.Core {
		header = "podlaz core logs"
	}
	if _, err := fmt.Fprintln(stdout, header); err != nil {
		return fmt.Errorf("write logs header: %w", err)
	}
	count, err := runJournalctl(ctx, stdout, opts)
	if err != nil {
		return err
	}
	if opts.Core && !opts.Follow && count == 0 {
		_, err := fmt.Fprintln(stdout, "No recent podlaz core logs found. Xray may be inactive, may have crashed before logging was configured, or the current user may not have access to the system journal. Run `podlaz status` for daemon state and `podlaz logs --daemon` for daemon lifecycle logs.")
		if err != nil {
			return fmt.Errorf("write missing core logs guidance: %w", err)
		}
	}
	return nil
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
	_, err := runJournalctl(ctx, stdout, opts)
	return err
}

func runJournalctl(ctx context.Context, stdout io.Writer, opts Options) (int, error) {
	cmd := exec.CommandContext(ctx, "journalctl", BuildJournalctlArgs(opts)...)

	outPipe, err := cmd.StdoutPipe()
	if err != nil {
		return 0, fmt.Errorf("prepare journalctl stdout: %w", err)
	}
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		return 0, fmt.Errorf("prepare journalctl stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start journalctl for %s: %w", DaemonUnit, err)
	}

	var filter func(string) bool
	if opts.Core {
		filter = isCoreLogLine
	}

	var stderr bytes.Buffer
	errc := make(chan scanResult, 2)
	go func() {
		count, err := scanRedactedFiltered(stdout, outPipe, filter)
		errc <- scanResult{name: "stdout", count: count, err: err}
	}()
	go func() {
		_, err := scanRedactedFiltered(&stderr, errPipe, nil)
		errc <- scanResult{name: "stderr", err: err}
	}()

	waitErr := cmd.Wait()
	var stdoutCount int
	for i := 0; i < 2; i++ {
		result := <-errc
		if result.err != nil {
			return stdoutCount, fmt.Errorf("read journalctl %s: %w", result.name, result.err)
		}
		if result.name == "stdout" {
			stdoutCount = result.count
		}
	}
	if waitErr != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = waitErr.Error()
		}
		return stdoutCount, fmt.Errorf("journalctl failed for %s: %s", DaemonUnit, render.Redact(message))
	}
	return stdoutCount, nil
}

type scanResult struct {
	name  string
	count int
	err   error
}

func scanRedacted(dst io.Writer, src io.Reader) error {
	_, err := scanRedactedFiltered(dst, src, nil)
	return err
}

func scanRedactedFiltered(dst io.Writer, src io.Reader, include func(string) bool) (int, error) {
	reader := bufio.NewReader(src)
	count := 0
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimRight(line, "\r\n")
			if include == nil || include(line) {
				if _, writeErr := fmt.Fprintln(dst, render.Redact(line)); writeErr != nil {
					return count, writeErr
				}
				count++
			}
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return count, nil
		}
		return count, err
	}
}

func isCoreLogLine(line string) bool {
	return strings.Contains(line, "podlazd: core xray ") ||
		strings.Contains(line, " xray[") ||
		strings.Contains(line, ": xray[")
}
