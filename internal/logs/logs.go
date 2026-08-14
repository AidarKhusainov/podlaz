package logs

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/render"
)

const (
	DaemonUnit       = "podlazd.service"
	DefaultLines     = "200"
	maxSinceDuration = 30 * 24 * time.Hour
)

var (
	ErrInvalidSinceDuration = errors.New("invalid logs --since duration")
	sinceDurationPattern    = regexp.MustCompile(`^[1-9][0-9]*[smh]$`)
)

// Options describes the read-only log stream requested by the CLI.
type Options struct {
	Follow bool
	Since  string
	Core   bool
}

// ParseSinceDuration validates the product-owned --since grammar. The public
// grammar is intentionally narrower than time.ParseDuration and journalctl:
// one positive decimal integer followed by exactly one of s, m, or h.
func ParseSinceDuration(value string) (string, error) {
	if !sinceDurationPattern.MatchString(value) {
		return "", fmt.Errorf("%w: expected <positive integer><s|m|h>", ErrInvalidSinceDuration)
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return "", fmt.Errorf("%w: duration is out of range", ErrInvalidSinceDuration)
	}
	if duration <= 0 {
		return "", fmt.Errorf("%w: duration must be positive", ErrInvalidSinceDuration)
	}
	if duration > maxSinceDuration {
		return "", fmt.Errorf("%w: duration must not exceed 720h", ErrInvalidSinceDuration)
	}
	return value, nil
}

// Run prints recent podlaz logs from journald.
func Run(ctx context.Context, stdout io.Writer, opts Options) error {
	args, err := buildJournalctlArgs(opts)
	if err != nil {
		return err
	}
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
	count, err := runJournalctl(ctx, stdout, args, opts.Core)
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

// BuildJournalctlArgs returns the exact journalctl argument vector for a valid
// product-level log request. Invalid --since values return nil and are surfaced
// as ErrInvalidSinceDuration by Run/RunJournalctl before journalctl is started.
func BuildJournalctlArgs(opts Options) []string {
	args, err := buildJournalctlArgs(opts)
	if err != nil {
		return nil
	}
	return args
}

func buildJournalctlArgs(opts Options) ([]string, error) {
	args := []string{
		"--system",
		"--unit", DaemonUnit,
		"--no-pager",
		"--output", "short",
	}
	if opts.Since != "" {
		since, err := ParseSinceDuration(opts.Since)
		if err != nil {
			return nil, err
		}
		// journalctl relative timestamps require an explicit sign. Podlaz owns
		// the input grammar and translates it to one argv value; no shell is used.
		args = append(args, "--since", "-"+since)
	} else {
		args = append(args, "--lines", DefaultLines)
	}
	if opts.Follow {
		args = append(args, "--follow")
	}
	return args, nil
}

// RunJournalctl executes journalctl and renders redacted output lines.
func RunJournalctl(ctx context.Context, stdout io.Writer, opts Options) error {
	args, err := buildJournalctlArgs(opts)
	if err != nil {
		return err
	}
	_, err = runJournalctl(ctx, stdout, args, opts.Core)
	return err
}

func runJournalctl(ctx context.Context, stdout io.Writer, args []string, core bool) (int, error) {
	cmd := exec.CommandContext(ctx, "journalctl", args...)

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
	if core {
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
