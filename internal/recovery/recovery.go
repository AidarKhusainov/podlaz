package recovery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultCommandTimeout = 3 * time.Second
	defaultRuntimeDir     = "/run/tunwarden"
	generatedDirName      = "generated"
	managedInterface      = "tunwarden0"
	managedNFTFamily      = "inet"
	managedNFTTableName   = "tunwarden"
	managedNFTTable       = managedNFTFamily + " " + managedNFTTableName
)

// Candidate describes one clearly TunWarden-owned resource that recover would
// clean up once explicit execution mode is implemented.
type Candidate struct {
	Kind        string
	Description string
	Target      string
}

// Warning describes a read-only recovery scan that could not complete.
type Warning struct {
	Target  string
	Message string
}

// ScanResult is the read-only snapshot used to build the recovery dry-run plan.
type ScanResult struct {
	Candidates []Candidate
	Warnings   []Warning
}

// Scanner inspects host state for clearly TunWarden-owned recovery candidates.
// Implementations must be strictly read-only.
type Scanner interface {
	Scan(ctx context.Context) ScanResult
}

// PlanResult is the dry-run representation of emergency recovery.
type PlanResult struct {
	Candidates []Candidate
	Warnings   []Warning
}

// Options controls recovery planning. Zero values use safe production defaults.
type Options struct {
	Scanner    Scanner
	Runner     CommandRunner
	RuntimeDir string
}

// CommandResult contains a completed command's observable output.
type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// CommandRunner is the read-only command execution abstraction used by recovery scanning.
type CommandRunner interface {
	LookPath(file string) (string, error)
	Run(ctx context.Context, name string, args ...string) (CommandResult, error)
}

// OSRunner executes read-only host commands through os/exec.
type OSRunner struct{}

// LookPath resolves a command using the host PATH.
func (OSRunner) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

// Run executes a host command and captures stdout, stderr, and exit code.
func (OSRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := CommandResult{
		Stdout:   strings.TrimSpace(stdout.String()),
		Stderr:   strings.TrimSpace(stderr.String()),
		ExitCode: 0,
	}
	if err == nil {
		return result, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
	} else {
		result.ExitCode = -1
	}

	return result, err
}

// Plan returns the current read-only recovery dry-run plan.
func Plan(ctx context.Context) PlanResult {
	return PlanWithOptions(ctx, Options{})
}

// PlanWithOptions builds a recovery dry-run plan with injectable dependencies for tests.
func PlanWithOptions(ctx context.Context, opts Options) PlanResult {
	scanner := opts.Scanner
	if scanner == nil {
		runtimeDir := opts.RuntimeDir
		if runtimeDir == "" {
			runtimeDir = defaultRuntimeDir
		}

		runner := opts.Runner
		if runner == nil {
			runner = OSRunner{}
		}

		scanner = OSScanner{
			Runner:     runner,
			RuntimeDir: runtimeDir,
		}
	}

	scan := scanner.Scan(ctx)
	return PlanResult{
		Candidates: append([]Candidate(nil), scan.Candidates...),
		Warnings:   append([]Warning(nil), scan.Warnings...),
	}
}

// String renders the recovery plan in a stable, CLI-friendly format.
func (p PlanResult) String() string {
	var b strings.Builder
	b.WriteString("TunWarden recovery dry-run\n")
	switch {
	case len(p.Candidates) > 0:
		for _, candidate := range p.Candidates {
			fmt.Fprintf(&b, "Would recover %s: %s\n", candidate.Description, candidate.Target)
		}
	case len(p.Warnings) == 0:
		b.WriteString("No TunWarden-owned recovery candidates found.\n")
	}
	for _, warning := range p.Warnings {
		fmt.Fprintf(&b, "Warning: could not inspect %s: %s\n", warning.Target, warning.Message)
	}
	b.WriteString("No changes were applied.\n")
	return b.String()
}

// OSScanner inspects the local host for clearly TunWarden-owned resources.
type OSScanner struct {
	Runner     CommandRunner
	RuntimeDir string
}

// Scan performs a strictly read-only recovery candidate scan.
func (s OSScanner) Scan(ctx context.Context) ScanResult {
	runner := s.Runner
	if runner == nil {
		runner = OSRunner{}
	}

	runtimeDir := s.RuntimeDir
	if runtimeDir == "" {
		runtimeDir = defaultRuntimeDir
	}

	var result ScanResult
	result.scanManagedInterface(ctx, runner)
	result.scanManagedNFTTable(ctx, runner)
	result.scanGeneratedRuntimeConfigs(filepath.Join(runtimeDir, generatedDirName))
	result.scanRuntimeDir(runtimeDir)
	return result
}

type commandCandidateScan struct {
	command            string
	commandUnavailable string
	args               []string
	candidate          Candidate
	warningTarget      string
}

func (r *ScanResult) scanManagedInterface(ctx context.Context, runner CommandRunner) {
	r.scanCommandCandidate(ctx, runner, commandCandidateScan{
		command:            "ip",
		commandUnavailable: "ip command is unavailable",
		args:               []string{"link", "show", "dev", managedInterface},
		candidate: Candidate{
			Kind:        "tun-interface",
			Description: "TUN interface",
			Target:      managedInterface,
		},
		warningTarget: "TUN interface " + managedInterface,
	})
}

func (r *ScanResult) scanManagedNFTTable(ctx context.Context, runner CommandRunner) {
	r.scanCommandCandidate(ctx, runner, commandCandidateScan{
		command:            "nft",
		commandUnavailable: "nft command is unavailable",
		args:               []string{"list", "table", managedNFTFamily, managedNFTTableName},
		candidate: Candidate{
			Kind:        "nftables-table",
			Description: "nftables table",
			Target:      managedNFTTable,
		},
		warningTarget: "nftables table " + managedNFTTable,
	})
}

func (r *ScanResult) scanCommandCandidate(ctx context.Context, runner CommandRunner, scan commandCandidateScan) {
	commandPath, err := runner.LookPath(scan.command)
	if err != nil {
		r.Warnings = append(r.Warnings, Warning{
			Target:  scan.warningTarget,
			Message: scan.commandUnavailable,
		})
		return
	}

	cmdResult, err := runCommand(ctx, runner, commandPath, scan.args...)
	switch {
	case commandSucceeded(cmdResult, err):
		r.Candidates = append(r.Candidates, scan.candidate)
	case resourceMissing(cmdResult):
	case commandFailedUnexpectedly(cmdResult, err):
		r.Warnings = append(r.Warnings, Warning{
			Target:  scan.warningTarget,
			Message: commandFailureMessage(cmdResult, err),
		})
	}
}

func (r *ScanResult) scanGeneratedRuntimeConfigs(generatedDir string) {
	stat, err := os.Stat(generatedDir)
	switch {
	case err == nil:
		description := "generated runtime configs"
		if !stat.IsDir() {
			description = "generated runtime config path"
		}
		r.Candidates = append(r.Candidates, Candidate{
			Kind:        "generated-runtime-configs",
			Description: description,
			Target:      generatedDir,
		})
	case errors.Is(err, os.ErrNotExist):
		return
	default:
		r.Warnings = append(r.Warnings, Warning{
			Target:  "generated runtime configs " + generatedDir,
			Message: err.Error(),
		})
	}
}

func (r *ScanResult) scanRuntimeDir(runtimeDir string) {
	stat, err := os.Stat(runtimeDir)
	switch {
	case err == nil:
		description := "runtime directory"
		if !stat.IsDir() {
			description = "runtime path"
		}
		r.Candidates = append(r.Candidates, Candidate{
			Kind:        "runtime-directory",
			Description: description,
			Target:      runtimeDir,
		})
	case errors.Is(err, os.ErrNotExist):
		return
	default:
		r.Warnings = append(r.Warnings, Warning{
			Target:  "runtime directory " + runtimeDir,
			Message: err.Error(),
		})
	}
}

func runCommand(ctx context.Context, runner CommandRunner, name string, args ...string) (CommandResult, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, defaultCommandTimeout)
	defer cancel()

	return runner.Run(cmdCtx, name, args...)
}

func commandSucceeded(result CommandResult, err error) bool {
	return err == nil && result.ExitCode == 0
}

func commandFailedUnexpectedly(result CommandResult, err error) bool {
	return err != nil || result.ExitCode != 0
}

func resourceMissing(result CommandResult) bool {
	if result.ExitCode == 0 {
		return false
	}
	text := strings.ToLower(result.Stdout + " " + result.Stderr)
	return strings.Contains(text, "does not exist") ||
		strings.Contains(text, "cannot find device") ||
		strings.Contains(text, "no such file or directory") ||
		strings.Contains(text, "no such table")
}

func commandFailureMessage(result CommandResult, err error) string {
	parts := make([]string, 0, 3)
	if result.ExitCode >= 0 {
		parts = append(parts, fmt.Sprintf("exit code %d", result.ExitCode))
	}
	if result.Stderr != "" {
		parts = append(parts, "stderr: "+singleLine(result.Stderr))
	}
	if err != nil && result.Stderr == "" {
		parts = append(parts, err.Error())
	}
	if len(parts) == 0 {
		return "command failed"
	}
	return strings.Join(parts, ", ")
}

func singleLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
