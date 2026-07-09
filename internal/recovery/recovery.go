package recovery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/render"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

const (
	defaultCommandTimeout = 3 * time.Second
	defaultRuntimeDir     = "/run/podlaz"
	generatedDirName      = "generated"
	managedInterface      = "podlaz0"
	managedNFTFamily      = "inet"
	managedNFTTableName   = "podlaz"
	managedNFTTable       = managedNFTFamily + " " + managedNFTTableName
	managedRouteTable     = "podlaz"
	managedRouteTableID   = "51820"
)

type Candidate struct {
	Kind        string `json:"kind"`
	Description string `json:"description"`
	Target      string `json:"target"`

	Transaction *TransactionCandidate `json:"transaction,omitempty"`
}

type TransactionCandidate struct {
	ID                string `json:"id"`
	State             string `json:"state"`
	Status            string `json:"status"`
	RollbackAvailable bool   `json:"rollback_available"`
	RequiresCleanup   bool   `json:"requires_cleanup"`
	Path              string `json:"path"`
}

type Warning struct {
	Target  string `json:"target"`
	Message string `json:"message"`
}

type ScanResult struct {
	Candidates []Candidate `json:"candidates"`
	Warnings   []Warning   `json:"warnings"`
}

type Scanner interface {
	Scan(ctx context.Context) ScanResult
}

type PlanResult struct {
	Candidates []Candidate `json:"candidates"`
	Warnings   []Warning   `json:"warnings"`
}

type CleanupResult struct {
	Candidate Candidate `json:"candidate"`
	Status    string    `json:"status"`
	Message   string    `json:"message,omitempty"`
}

type ExecuteResult struct {
	Results  []CleanupResult `json:"results"`
	Warnings []Warning       `json:"warnings"`
}

func (r ExecuteResult) HasFailures() bool {
	for _, result := range r.Results {
		if result.Status == "failed" {
			return true
		}
	}
	return false
}

func (r ExecuteResult) HasIncompleteCleanup() bool {
	for _, result := range r.Results {
		if result.Status == "skipped" && (result.Candidate.Kind == "transaction-state" || strings.Contains(result.Message, "transaction state was preserved")) {
			return true
		}
	}
	return false
}

type CleanupExecutor interface {
	Cleanup(ctx context.Context, candidate Candidate) CleanupResult
}

type MultiCleanupExecutor interface {
	CleanupMany(ctx context.Context, candidate Candidate) []CleanupResult
}

type Options struct {
	Scanner    Scanner
	Runner     CommandRunner
	Executor   CleanupExecutor
	RuntimeDir string
}

type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type CommandRunner interface {
	LookPath(file string) (string, error)
	Run(ctx context.Context, name string, args ...string) (CommandResult, error)
}

type OSRunner struct{}

func (OSRunner) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

func (OSRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := CommandResult{Stdout: strings.TrimSpace(stdout.String()), Stderr: strings.TrimSpace(stderr.String())}
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

func Plan(ctx context.Context) PlanResult {
	return PlanWithOptions(ctx, Options{})
}

func PlanWithOptions(ctx context.Context, opts Options) PlanResult {
	scan := recoveryScanner(opts).Scan(ctx)
	return PlanResult{Candidates: append([]Candidate(nil), scan.Candidates...), Warnings: append([]Warning(nil), scan.Warnings...)}
}

func Execute(ctx context.Context) ExecuteResult {
	return ExecuteWithOptions(ctx, Options{})
}

func ExecuteWithOptions(ctx context.Context, opts Options) ExecuteResult {
	plan := PlanWithOptions(ctx, opts)
	ordered := orderCleanupCandidates(plan.Candidates)
	results := make([]CleanupResult, 0, len(ordered))
	if opts.Executor == nil {
		for _, candidate := range ordered {
			results = append(results, failed(candidate, errors.New("missing daemon-owned recovery cleanup executor")))
		}
		return ExecuteResult{Results: results, Warnings: append([]Warning(nil), plan.Warnings...)}
	}
	if multi, ok := opts.Executor.(MultiCleanupExecutor); ok {
		for _, candidate := range ordered {
			results = append(results, multi.CleanupMany(ctx, candidate)...)
		}
		return ExecuteResult{Results: results, Warnings: append([]Warning(nil), plan.Warnings...)}
	}
	for _, candidate := range ordered {
		results = append(results, opts.Executor.Cleanup(ctx, candidate))
	}
	return ExecuteResult{Results: results, Warnings: append([]Warning(nil), plan.Warnings...)}
}

func (p PlanResult) String() string {
	var b strings.Builder
	b.WriteString("podlaz recovery dry-run\n")
	b.WriteString("Inspection: read-only; uses daemon startup scan when available plus local safe checks. Local permission warnings can differ from daemon-owned --execute cleanup.\n")
	switch {
	case len(p.Candidates) > 0:
		for _, candidate := range p.Candidates {
			if candidate.Transaction != nil {
				writeTransactionCandidate(&b, candidate.Transaction)
				continue
			}
			fmt.Fprintf(&b, "Would recover %s: %s\n", safeText(candidate.Description), safeText(candidate.Target))
		}
	case len(p.Warnings) == 0:
		b.WriteString("No podlaz-owned recovery candidates found.\n")
	}
	for _, warning := range p.Warnings {
		fmt.Fprintf(&b, "Warning: could not inspect %s: %s\n", safeText(warning.Target), safeText(warning.Message))
	}
	b.WriteString("No changes were applied.\n")
	return b.String()
}

func (r ExecuteResult) String() string {
	var b strings.Builder
	b.WriteString("podlaz recovery\n")
	b.WriteString("Mode: execute\n")
	if len(r.Results) == 0 && len(r.Warnings) == 0 {
		b.WriteString("No podlaz-owned recovery candidates found.\n")
	}
	for _, result := range r.Results {
		switch result.Status {
		case "recovered":
			fmt.Fprintf(&b, "Recovered %s: %s\n", safeText(result.Candidate.Description), safeText(result.Candidate.Target))
		case "skipped":
			fmt.Fprintf(&b, "Skipped %s: %s", safeText(result.Candidate.Description), safeText(result.Candidate.Target))
			if result.Message != "" {
				fmt.Fprintf(&b, " (%s)", safeText(result.Message))
			}
			b.WriteByte('\n')
		case "failed":
			fmt.Fprintf(&b, "Failed to recover %s: %s", safeText(result.Candidate.Description), safeText(result.Candidate.Target))
			if result.Message != "" {
				fmt.Fprintf(&b, ": %s", safeText(result.Message))
			}
			b.WriteByte('\n')
		}
	}
	for _, warning := range r.Warnings {
		fmt.Fprintf(&b, "Warning: could not inspect %s: %s\n", safeText(warning.Target), safeText(warning.Message))
	}
	b.WriteString("No non-podlaz resources were touched.\n")
	return b.String()
}

func writeTransactionCandidate(b *strings.Builder, tx *TransactionCandidate) {
	fmt.Fprintf(b, "Transaction: %s\n", safeText(tx.Status))
	fmt.Fprintf(b, "Rollback available: %s\n", yesNo(tx.RollbackAvailable))
	fmt.Fprintf(b, "State path: %s\n", safeText(tx.Path))
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func safeText(s string) string {
	return render.Redact(s)
}

func recoveryScanner(opts Options) Scanner {
	if opts.Scanner != nil {
		return opts.Scanner
	}
	runner := opts.Runner
	if runner == nil {
		runner = OSRunner{}
	}
	return OSScanner{Runner: runner, RuntimeDir: runtimeDir(opts.RuntimeDir)}
}

func runtimeDir(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return defaultRuntimeDir
	}
	return filepath.Clean(dir)
}

type OSScanner struct {
	Runner     CommandRunner
	RuntimeDir string
}

func (s OSScanner) Scan(ctx context.Context) ScanResult {
	runner := s.Runner
	if runner == nil {
		runner = OSRunner{}
	}
	return ScanResult{Candidates: scanCandidates(ctx, runner, s.RuntimeDir), Warnings: scanWarnings(ctx, runner, s.RuntimeDir)}
}

func scanCandidates(ctx context.Context, runner CommandRunner, runtimeDir string) []Candidate {
	var candidates []Candidate
	if exists(ctx, runner, "ip", "link", "show", "dev", managedInterface) {
		candidates = append(candidates, Candidate{Kind: "interface", Description: "interface " + managedInterface, Target: managedInterface})
	}
	if exists(ctx, runner, "nft", "list", "table", managedNFTFamily, managedNFTTableName) {
		candidates = append(candidates, Candidate{Kind: "nftables-table", Description: "nft table " + managedNFTTable, Target: managedNFTTable})
	}
	candidates = append(candidates, transactionCandidates(runtimeDir)...)
	return candidates
}

func transactionCandidates(runtimeDir string) []Candidate {
	summaries, _ := txstate.ScanTransactions(runtimeDir)
	candidates := make([]Candidate, 0, len(summaries))
	for _, summary := range summaries {
		if !summary.RequiresCleanup {
			continue
		}
		candidates = append(candidates, Candidate{
			Kind:        "transaction-state",
			Description: "transaction rollback state",
			Target:      summary.Path,
			Transaction: &TransactionCandidate{
				ID:                summary.ID,
				State:             string(summary.State),
				Status:            summary.StatusLine(),
				RollbackAvailable: summary.RollbackAvailable,
				RequiresCleanup:   summary.RequiresCleanup,
				Path:              summary.Path,
			},
		})
	}
	return candidates
}

func scanWarnings(ctx context.Context, runner CommandRunner, runtimeDir string) []Warning {
	var warnings []Warning
	for _, command := range []struct {
		target string
		name   string
		args   []string
	}{
		{target: "interface " + managedInterface, name: "ip", args: []string{"link", "show", "dev", managedInterface}},
		{target: "nft table " + managedNFTTable, name: "nft", args: []string{"list", "table", managedNFTFamily, managedNFTTableName}},
	} {
		if _, err := runner.LookPath(command.name); err != nil {
			warnings = append(warnings, Warning{Target: command.target, Message: command.name + " not found"})
			continue
		}
		result, err := run(ctx, runner, command.name, command.args...)
		if err == nil || resourceMissing(result) {
			continue
		}
		warnings = append(warnings, Warning{Target: command.target, Message: commandFailureMessage(result, err)})
	}
	_, txWarnings := txstate.ScanTransactions(runtimeDir)
	for _, warning := range txWarnings {
		warnings = append(warnings, Warning{Target: "transaction state", Message: warning})
	}
	return warnings
}

func exists(ctx context.Context, runner CommandRunner, name string, args ...string) bool {
	if _, err := runner.LookPath(name); err != nil {
		return false
	}
	result, err := run(ctx, runner, name, args...)
	return err == nil && result.ExitCode == 0
}

func run(ctx context.Context, runner CommandRunner, name string, args ...string) (CommandResult, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, defaultCommandTimeout)
	defer cancel()
	path, err := runner.LookPath(name)
	if err != nil {
		return CommandResult{ExitCode: -1}, err
	}
	return runner.Run(cmdCtx, path, args...)
}

func resourceMissing(result CommandResult) bool {
	text := strings.ToLower(result.Stdout + " " + result.Stderr)
	return strings.Contains(text, "does not exist") ||
		strings.Contains(text, "cannot find device") ||
		strings.Contains(text, "no such file or directory") ||
		strings.Contains(text, "no such table")
}

func commandFailureMessage(result CommandResult, err error) string {
	parts := make([]string, 0, 3)
	if result.ExitCode >= 0 {
		parts = append(parts, "exit code "+strconv.Itoa(result.ExitCode))
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

func failed(candidate Candidate, err error) CleanupResult {
	return CleanupResult{Candidate: candidate, Status: "failed", Message: err.Error()}
}

func recovered(candidate Candidate) CleanupResult {
	return CleanupResult{Candidate: candidate, Status: "recovered"}
}

func skipped(candidate Candidate, message string) CleanupResult {
	return CleanupResult{Candidate: candidate, Status: "skipped", Message: message}
}
