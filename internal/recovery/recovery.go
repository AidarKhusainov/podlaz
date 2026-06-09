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
	"syscall"
	"time"

	"github.com/AidarKhusainov/tunwarden/internal/render"
	txstate "github.com/AidarKhusainov/tunwarden/internal/state"
)

const (
	defaultCommandTimeout = 3 * time.Second
	defaultRuntimeDir     = "/run/tunwarden"
	generatedDirName      = "generated"
	managedInterface      = "tunwarden0"
	managedNFTFamily      = "inet"
	managedNFTTableName   = "tunwarden"
	managedNFTTable       = managedNFTFamily + " " + managedNFTTableName
	managedRouteTable     = "tunwarden"
	managedRouteTableID   = "51820"
)

// Candidate describes one clearly TunWarden-owned resource that recover can
// clean up only in explicit execute mode.
type Candidate struct {
	Kind        string `json:"kind"`
	Description string `json:"description"`
	Target      string `json:"target"`

	Transaction *TransactionCandidate `json:"transaction,omitempty"`
}

// TransactionCandidate describes pending or stale transaction state in the
// recovery model without exposing full rollback metadata or secrets.
type TransactionCandidate struct {
	ID                string `json:"id"`
	State             string `json:"state"`
	Status            string `json:"status"`
	RollbackAvailable bool   `json:"rollback_available"`
	RequiresCleanup   bool   `json:"requires_cleanup"`
	Path              string `json:"path"`
}

// Warning describes a read-only recovery scan that could not complete.
type Warning struct {
	Target  string `json:"target"`
	Message string `json:"message"`
}

// ScanResult is the read-only snapshot used to build the recovery dry-run plan.
type ScanResult struct {
	Candidates []Candidate `json:"candidates"`
	Warnings   []Warning   `json:"warnings"`
}

// Scanner inspects host state for clearly TunWarden-owned recovery candidates.
// Implementations must be strictly read-only.
type Scanner interface {
	Scan(ctx context.Context) ScanResult
}

// PlanResult is the dry-run representation of emergency recovery.
type PlanResult struct {
	Candidates []Candidate `json:"candidates"`
	Warnings   []Warning   `json:"warnings"`
}

// CleanupResult describes one attempted explicit cleanup action.
type CleanupResult struct {
	Candidate Candidate `json:"candidate"`
	Status    string    `json:"status"`
	Message   string    `json:"message,omitempty"`
}

// ExecuteResult is the explicit recovery execution report.
type ExecuteResult struct {
	Results  []CleanupResult `json:"results"`
	Warnings []Warning       `json:"warnings"`
}

// HasFailures reports whether cleanup execution encountered a failed action.
func (r ExecuteResult) HasFailures() bool {
	for _, result := range r.Results {
		if result.Status == "failed" {
			return true
		}
	}
	return false
}

// CleanupExecutor performs explicit cleanup for already-detected candidates.
type CleanupExecutor interface {
	Cleanup(ctx context.Context, candidate Candidate) CleanupResult
}

// Options controls recovery planning and execution. Zero values use safe
// production defaults.
type Options struct {
	Scanner    Scanner
	Runner     CommandRunner
	Executor   CleanupExecutor
	RuntimeDir string
}

// CommandResult contains a completed command's observable output.
type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// CommandRunner is the command execution abstraction used by recovery scanning
// and explicit cleanup execution.
type CommandRunner interface {
	LookPath(file string) (string, error)
	Run(ctx context.Context, name string, args ...string) (CommandResult, error)
}

// OSRunner executes host commands through os/exec.
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
	scanner := recoveryScanner(opts)
	scan := scanner.Scan(ctx)
	return PlanResult{
		Candidates: append([]Candidate(nil), scan.Candidates...),
		Warnings:   append([]Warning(nil), scan.Warnings...),
	}
}

// Execute performs explicit recovery cleanup for the current recovery plan.
func Execute(ctx context.Context) ExecuteResult {
	return ExecuteWithOptions(ctx, Options{})
}

// ExecuteWithOptions performs explicit recovery cleanup with injectable dependencies for tests.
func ExecuteWithOptions(ctx context.Context, opts Options) ExecuteResult {
	plan := PlanWithOptions(ctx, opts)
	executor := opts.Executor
	if executor == nil {
		runner := opts.Runner
		if runner == nil {
			runner = OSRunner{}
		}
		executor = OSCleanupExecutor{Runner: runner, RuntimeDir: runtimeDir(opts.RuntimeDir)}
	}

	ordered := orderCleanupCandidates(plan.Candidates)
	results := make([]CleanupResult, 0, len(ordered))
	for _, candidate := range ordered {
		results = append(results, executor.Cleanup(ctx, candidate))
	}
	return ExecuteResult{Results: results, Warnings: append([]Warning(nil), plan.Warnings...)}
}

// String renders the recovery plan in a stable, CLI-friendly format.
func (p PlanResult) String() string {
	var b strings.Builder
	b.WriteString("TunWarden recovery dry-run\n")
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
		b.WriteString("No TunWarden-owned recovery candidates found.\n")
	}
	for _, warning := range p.Warnings {
		fmt.Fprintf(&b, "Warning: could not inspect %s: %s\n", safeText(warning.Target), safeText(warning.Message))
	}
	b.WriteString("No changes were applied.\n")
	return b.String()
}

// String renders the explicit recovery execution report.
func (r ExecuteResult) String() string {
	var b strings.Builder
	b.WriteString("TunWarden recovery\n")
	b.WriteString("Mode: execute\n")
	if len(r.Results) == 0 && len(r.Warnings) == 0 {
		b.WriteString("No TunWarden-owned recovery candidates found.\n")
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
	b.WriteString("No non-TunWarden resources were touched.\n")
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

	runtimeDir := runtimeDir(s.RuntimeDir)

	var result ScanResult
	result.scanManagedInterface(ctx, runner)
	result.scanManagedNFTTable(ctx, runner)
	result.scanTransactionState(runtimeDir)
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

func (r *ScanResult) scanTransactionState(runtimeDir string) {
	summaries, warnings := txstate.ScanTransactions(runtimeDir)
	for _, summary := range summaries {
		if !summary.RequiresCleanup {
			continue
		}
		r.Candidates = append(r.Candidates, Candidate{
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
	for _, warning := range warnings {
		r.Warnings = append(r.Warnings, Warning{Target: "transaction state", Message: warning})
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

// OSCleanupExecutor removes only allowlisted TunWarden-owned volatile state.
type OSCleanupExecutor struct {
	Runner     CommandRunner
	RuntimeDir string
}

// Cleanup executes one idempotent cleanup action.
func (e OSCleanupExecutor) Cleanup(ctx context.Context, candidate Candidate) CleanupResult {
	if strings.TrimSpace(candidate.Kind) == "" {
		return skipped(candidate, "missing recovery candidate kind")
	}
	if e.Runner == nil {
		e.Runner = OSRunner{}
	}
	if strings.TrimSpace(e.RuntimeDir) == "" {
		e.RuntimeDir = defaultRuntimeDir
	}
	e.RuntimeDir = filepath.Clean(e.RuntimeDir)

	switch candidate.Kind {
	case "tun-interface":
		return e.cleanupTUNInterface(ctx, candidate)
	case "nftables-table":
		return e.cleanupNFTablesTable(ctx, candidate)
	case "transaction-state":
		return e.cleanupTransactionState(ctx, candidate)
	case "generated-runtime-configs":
		return e.cleanupGeneratedRuntimeConfigs(candidate)
	case "runtime-directory":
		return e.cleanupRuntimeDirectory(candidate)
	default:
		return skipped(candidate, "unsupported recovery candidate kind")
	}
}

func (e OSCleanupExecutor) cleanupTUNInterface(ctx context.Context, candidate Candidate) CleanupResult {
	if candidate.Target != managedInterface {
		return skipped(candidate, "non-TunWarden TUN interface target")
	}
	if err := e.run(ctx, "ip", "link", "del", "dev", managedInterface); err != nil && !commandErrorIsMissing(err) {
		return failed(candidate, err)
	}
	return recovered(candidate)
}

func (e OSCleanupExecutor) cleanupNFTablesTable(ctx context.Context, candidate Candidate) CleanupResult {
	family, table, ok := parseNFTTarget(candidate.Target)
	if !ok || !isManagedNFTTarget(family, table) {
		return skipped(candidate, "non-TunWarden nftables target")
	}
	if err := e.run(ctx, "nft", "delete", "table", family, table); err != nil && !commandErrorIsMissing(err) {
		return failed(candidate, err)
	}
	return recovered(candidate)
}

func (e OSCleanupExecutor) cleanupGeneratedRuntimeConfigs(candidate Candidate) CleanupResult {
	generatedDir := filepath.Join(e.RuntimeDir, generatedDirName)
	if !sameCleanPath(candidate.Target, generatedDir) {
		return skipped(candidate, "generated runtime config path is outside TunWarden runtime state")
	}
	if err := os.RemoveAll(generatedDir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return failed(candidate, fmt.Errorf("remove generated runtime configs %s: %w", generatedDir, err))
	}
	return recovered(candidate)
}

func (e OSCleanupExecutor) cleanupRuntimeDirectory(candidate Candidate) CleanupResult {
	if !sameCleanPath(candidate.Target, e.RuntimeDir) {
		return skipped(candidate, "runtime path is outside TunWarden runtime state")
	}
	if err := os.RemoveAll(e.RuntimeDir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return failed(candidate, fmt.Errorf("remove runtime directory %s: %w", e.RuntimeDir, err))
	}
	return recovered(candidate)
}

func (e OSCleanupExecutor) cleanupTransactionState(ctx context.Context, candidate Candidate) CleanupResult {
	if candidate.Transaction == nil {
		return skipped(candidate, "missing transaction summary")
	}
	path := filepath.Clean(candidate.Transaction.Path)
	if !sameCleanPath(path, candidate.Target) || !isTransactionPath(e.RuntimeDir, path) {
		return skipped(candidate, "transaction path is outside TunWarden runtime state")
	}
	tx, err := txstate.LoadTransactionFile(path)
	if err != nil {
		return failed(candidate, fmt.Errorf("load transaction state: %w", err))
	}
	if !tx.RequiresCleanup() {
		return recovered(candidate)
	}
	if err := e.rollbackTransaction(ctx, tx); err != nil {
		return failed(candidate, err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return failed(candidate, fmt.Errorf("remove transaction state %s: %w", path, err))
	}
	return recovered(candidate)
}

func (e OSCleanupExecutor) rollbackTransaction(ctx context.Context, tx txstate.Transaction) error {
	var errs []error
	for i := len(tx.Rollback.ChildProcesses) - 1; i >= 0; i-- {
		if err := e.stopChildProcess(tx.Rollback.ChildProcesses[i]); err != nil {
			errs = append(errs, err)
		}
	}
	if err := e.rollbackNFTables(ctx, tx.Rollback.NFTables); err != nil {
		errs = append(errs, err)
	}
	for i := len(tx.Rollback.DNS) - 1; i >= 0; i-- {
		if err := e.rollbackDNS(ctx, tx.Rollback.DNS[i]); err != nil {
			errs = append(errs, err)
		}
	}
	for i := len(tx.Rollback.PolicyRules) - 1; i >= 0; i-- {
		if err := e.rollbackPolicyRule(ctx, tx.Rollback.PolicyRules[i]); err != nil {
			errs = append(errs, err)
		}
	}
	for i := len(tx.Rollback.Routes) - 1; i >= 0; i-- {
		if err := e.rollbackRoute(ctx, tx.Rollback.Routes[i]); err != nil {
			errs = append(errs, err)
		}
	}
	for i := len(tx.Rollback.TUN) - 1; i >= 0; i-- {
		if err := e.rollbackTUN(ctx, tx.Rollback.TUN[i]); err != nil {
			errs = append(errs, err)
		}
	}
	for i := len(tx.Rollback.GeneratedConfigs) - 1; i >= 0; i-- {
		if err := e.removeGeneratedConfig(tx.Rollback.GeneratedConfigs[i]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (e OSCleanupExecutor) stopChildProcess(proc txstate.ChildProcessRollback) error {
	if proc.Owner != txstate.TransactionOwner {
		return fmt.Errorf("refuse to stop non-TunWarden child process label=%q pid=%d", proc.Label, proc.PID)
	}
	if !isAllowedChildProcessLabel(proc.Label) {
		return fmt.Errorf("refuse to stop ambiguous child process label=%q pid=%d", proc.Label, proc.PID)
	}
	if proc.PID <= 1 {
		return nil
	}
	process, err := os.FindProcess(proc.PID)
	if err != nil {
		return fmt.Errorf("find child process %d: %w", proc.PID, err)
	}
	if err := process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) && !errorStringContains(err, "no such process") {
		return fmt.Errorf("stop child process %s pid %d: %w", proc.Label, proc.PID, err)
	}
	return nil
}

func (e OSCleanupExecutor) rollbackNFTables(ctx context.Context, entries []txstate.NFTablesRollback) error {
	seen := make(map[string]struct{})
	var errs []error
	for _, entry := range entries {
		if entry.Owner != txstate.TransactionOwner {
			errs = append(errs, fmt.Errorf("refuse to rollback non-TunWarden nftables target %s %s", entry.Family, entry.Table))
			continue
		}
		if !isManagedNFTTarget(entry.Family, entry.Table) {
			errs = append(errs, fmt.Errorf("refuse to rollback non-TunWarden nftables target %s %s", entry.Family, entry.Table))
			continue
		}
		key := entry.Family + " " + entry.Table
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if err := e.run(ctx, "nft", "delete", "table", managedNFTFamily, managedNFTTableName); err != nil && !commandErrorIsMissing(err) {
			errs = append(errs, fmt.Errorf("delete nftables table %s: %w", managedNFTTable, err))
		}
	}
	return errors.Join(errs...)
}

func (e OSCleanupExecutor) rollbackDNS(ctx context.Context, dns txstate.DNSRollback) error {
	if dns.Owner != txstate.TransactionOwner {
		return fmt.Errorf("refuse to rollback non-TunWarden DNS target %s", dns.Link)
	}
	if dns.Link != managedInterface {
		return fmt.Errorf("refuse to rollback non-TunWarden DNS link %s", dns.Link)
	}
	if dns.Backend != "" && dns.Backend != "systemd-resolved" {
		return fmt.Errorf("refuse to rollback unsupported DNS backend %s", dns.Backend)
	}
	if err := e.run(ctx, "resolvectl", "revert", managedInterface); err != nil && !commandErrorIsMissing(err) {
		return fmt.Errorf("revert systemd-resolved DNS for %s: %w", managedInterface, err)
	}
	return nil
}

func (e OSCleanupExecutor) rollbackPolicyRule(ctx context.Context, rule txstate.PolicyRuleRollback) error {
	if rule.Owner != txstate.TransactionOwner {
		return fmt.Errorf("refuse to rollback non-TunWarden policy rule priority %d", rule.Priority)
	}
	if rule.Priority <= 0 {
		return nil
	}
	table, ok := managedTableToken(rule.Table)
	if !ok {
		return fmt.Errorf("refuse to rollback policy rule priority %d with non-TunWarden table %s", rule.Priority, rule.Table)
	}
	args := []string{"-4", "rule", "del", "priority", strconv.Itoa(rule.Priority)}
	if strings.TrimSpace(rule.From) != "" {
		args = append(args, "from", strings.TrimSpace(rule.From))
	}
	if strings.TrimSpace(rule.To) != "" {
		args = append(args, "to", strings.TrimSpace(rule.To))
	}
	if strings.TrimSpace(rule.Mark) != "" {
		args = append(args, "fwmark", strings.TrimSpace(rule.Mark))
	}
	args = append(args, "lookup", table)
	if err := e.run(ctx, "ip", args...); err != nil && !commandErrorIsMissing(err) {
		return fmt.Errorf("delete policy rule priority %d: %w", rule.Priority, err)
	}
	return nil
}

func (e OSCleanupExecutor) rollbackRoute(ctx context.Context, route txstate.RouteRollback) error {
	if route.Owner != txstate.TransactionOwner {
		return fmt.Errorf("refuse to rollback non-TunWarden route %s table %s", route.CIDR, route.Table)
	}
	table, ok := managedTableToken(route.Table)
	if !ok {
		return fmt.Errorf("refuse to rollback route %s with non-TunWarden table %s", route.CIDR, route.Table)
	}
	cidr := strings.TrimSpace(route.CIDR)
	if cidr == "" {
		return nil
	}
	args := []string{"-4", "route", "del", cidr}
	if strings.TrimSpace(route.Via) != "" {
		args = append(args, "via", strings.TrimSpace(route.Via))
	}
	if strings.TrimSpace(route.Dev) != "" {
		if route.Dev != managedInterface {
			return fmt.Errorf("refuse to rollback route %s with non-TunWarden device %s", route.CIDR, route.Dev)
		}
		args = append(args, "dev", route.Dev)
	}
	args = append(args, "table", table)
	if err := e.run(ctx, "ip", args...); err != nil && !commandErrorIsMissing(err) {
		return fmt.Errorf("delete route %s table %s: %w", route.CIDR, route.Table, err)
	}
	return nil
}

func (e OSCleanupExecutor) rollbackTUN(ctx context.Context, tun txstate.TUNRollback) error {
	if tun.Owner != txstate.TransactionOwner {
		return fmt.Errorf("refuse to rollback non-TunWarden TUN interface %s", tun.InterfaceName)
	}
	if tun.InterfaceName != managedInterface {
		return fmt.Errorf("refuse to rollback non-TunWarden TUN interface %s", tun.InterfaceName)
	}
	if err := e.run(ctx, "ip", "link", "del", "dev", managedInterface); err != nil && !commandErrorIsMissing(err) {
		return fmt.Errorf("delete TUN device %s: %w", managedInterface, err)
	}
	return nil
}

func (e OSCleanupExecutor) removeGeneratedConfig(config txstate.GeneratedConfigRollback) error {
	if config.Owner != txstate.TransactionOwner {
		return fmt.Errorf("refuse to remove non-TunWarden generated config %s", config.Path)
	}
	path := filepath.Clean(config.Path)
	if !isUnderDir(filepath.Join(e.RuntimeDir, generatedDirName), path) {
		return fmt.Errorf("refuse to remove generated config outside TunWarden runtime state: %s", config.Path)
	}
	if err := os.RemoveAll(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove generated config %s: %w", path, err)
	}
	return nil
}

func (e OSCleanupExecutor) run(ctx context.Context, command string, args ...string) error {
	path, err := e.Runner.LookPath(command)
	if err != nil {
		return fmt.Errorf("%s command is unavailable", command)
	}
	_, err = runCommand(ctx, e.Runner, path, args...)
	return err
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

func commandErrorIsMissing(err error) bool {
	return errorStringContains(err, "does not exist", "cannot find device", "no such process", "no such file or directory", "no such table", "no such file")
}

func errorStringContains(err error, needles ...string) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func recovered(candidate Candidate) CleanupResult {
	return CleanupResult{Candidate: candidate, Status: "recovered"}
}

func skipped(candidate Candidate, message string) CleanupResult {
	return CleanupResult{Candidate: candidate, Status: "skipped", Message: message}
}

func failed(candidate Candidate, err error) CleanupResult {
	return CleanupResult{Candidate: candidate, Status: "failed", Message: err.Error()}
}

func orderCleanupCandidates(candidates []Candidate) []Candidate {
	ordered := append([]Candidate(nil), candidates...)
	weight := func(kind string) int {
		switch kind {
		case "transaction-state":
			return 10
		case "nftables-table":
			return 20
		case "tun-interface":
			return 30
		case "generated-runtime-configs":
			return 40
		case "runtime-directory":
			return 50
		default:
			return 100
		}
	}
	for i := 1; i < len(ordered); i++ {
		for j := i; j > 0 && weight(ordered[j-1].Kind) > weight(ordered[j].Kind); j-- {
			ordered[j-1], ordered[j] = ordered[j], ordered[j-1]
		}
	}
	return ordered
}

func parseNFTTarget(target string) (string, string, bool) {
	fields := strings.Fields(target)
	if len(fields) != 2 {
		return "", "", false
	}
	return fields[0], fields[1], true
}

func isManagedNFTTarget(family, table string) bool {
	return strings.TrimSpace(family) == managedNFTFamily && strings.TrimSpace(table) == managedNFTTableName
}

func managedTableToken(table string) (string, bool) {
	table = strings.TrimSpace(table)
	switch table {
	case managedRouteTable, managedRouteTableID:
		return managedRouteTableID, true
	default:
		return "", false
	}
}

func isAllowedChildProcessLabel(label string) bool {
	switch strings.TrimSpace(label) {
	case "xray", "tun2socks":
		return true
	default:
		return false
	}
}

func isTransactionPath(runtimeDir, path string) bool {
	transactionsDir := filepath.Join(runtimeDir, txstate.TransactionDirName)
	if !isUnderDir(transactionsDir, path) {
		return false
	}
	return strings.HasSuffix(path, txstate.TransactionFileSuffix)
}

func isUnderDir(dir, path string) bool {
	dir = filepath.Clean(dir)
	path = filepath.Clean(path)
	if path == dir {
		return true
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

func sameCleanPath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}
