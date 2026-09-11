package recovery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

type rolledBackTransactionAbsenceOptions struct {
	Runner     CommandRunner
	PathExists func(string) (bool, error)
	ReadFile   func(string) ([]byte, error)
}

// VerifyRolledBackTransactionAbsence performs a read-only, transaction-bound
// observation of the exact resources a completed rollback claims to have
// removed. The retained rolled-back transaction is evidence only: this function
// never turns observation into cleanup authority and never mutates host state.
func VerifyRolledBackTransactionAbsence(ctx context.Context, runtimeDir, transactionID string) error {
	return verifyRolledBackTransactionAbsenceWithOptions(ctx, runtimeDir, transactionID, rolledBackTransactionAbsenceOptions{})
}

func verifyRolledBackTransactionAbsenceWithOptions(
	ctx context.Context,
	runtimeDir string,
	transactionID string,
	opts rolledBackTransactionAbsenceOptions,
) error {
	if ctx == nil || ctx.Err() != nil {
		return errors.New("rolled-back transaction absence proof requires a live context")
	}
	runtimeDir = runtimeDirOrDefault(runtimeDir)
	transactionID = strings.TrimSpace(transactionID)
	if transactionID == "" {
		return errors.New("rolled-back transaction absence proof requires an exact transaction id")
	}
	if opts.Runner == nil {
		opts.Runner = OSRunner{}
	}
	if opts.PathExists == nil {
		opts.PathExists = lstatPathExists
	}
	if opts.ReadFile == nil {
		opts.ReadFile = os.ReadFile
	}

	store := txstate.TransactionStore{RuntimeDir: runtimeDir}
	tx, _, err := store.Load(transactionID)
	if err != nil {
		return fmt.Errorf("load retained rolled-back transaction evidence: %w", err)
	}
	if tx.State != txstate.TransactionRolledBack || tx.RequiresRecovery() {
		return fmt.Errorf("retained transaction %s is not conclusively rolled back", transactionID)
	}
	if reasons := rollbackOwnershipConsistencyReasons(tx, tx.Rollback); len(reasons) != 0 {
		return fmt.Errorf("retained rolled-back transaction ownership is inconsistent: %s", strings.Join(reasons, "; "))
	}
	if err := verifyNoOtherTransactionAuthority(runtimeDir, transactionID); err != nil {
		return err
	}
	if err := verifyRolledBackLinkAbsent(ctx, opts.Runner, tx.Rollback); err != nil {
		return err
	}
	if err := verifyRolledBackRoutesAbsent(ctx, opts.Runner, tx.Rollback.Routes); err != nil {
		return err
	}
	if err := verifyRolledBackPolicyRulesAbsent(ctx, opts.Runner, tx.Rollback.PolicyRules); err != nil {
		return err
	}
	if err := verifyRolledBackDNSAbsent(ctx, opts.Runner, tx.Rollback.DNS); err != nil {
		return err
	}
	if err := verifyRolledBackNFTablesAbsent(ctx, opts.Runner, tx.Rollback.NFTables); err != nil {
		return err
	}
	if err := verifyRolledBackChildProcessesAbsent(tx, opts.ReadFile); err != nil {
		return err
	}
	if err := verifyRolledBackGeneratedConfigsAbsent(runtimeDir, tx, opts.PathExists); err != nil {
		return err
	}
	return nil
}

func runtimeDirOrDefault(runtimeDir string) string {
	if strings.TrimSpace(runtimeDir) == "" {
		return defaultRuntimeDir
	}
	return filepath.Clean(runtimeDir)
}

func lstatPathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	default:
		return false, err
	}
}

func verifyNoOtherTransactionAuthority(runtimeDir, retainedID string) error {
	summaries, warnings := txstate.ScanTransactions(runtimeDir)
	if len(warnings) != 0 {
		return fmt.Errorf("transaction authority inspection is inconclusive: %s", strings.Join(warnings, "; "))
	}
	retainedFound := false
	for _, summary := range summaries {
		if summary.ID == retainedID {
			retainedFound = true
			if summary.State != txstate.TransactionRolledBack || summary.RequiresRecovery {
				return fmt.Errorf("retained transaction %s regained cleanup authority", retainedID)
			}
			continue
		}
		return fmt.Errorf("another durable transaction authority remains: %s (%s)", summary.ID, summary.State)
	}
	if !retainedFound {
		return fmt.Errorf("retained rolled-back transaction %s disappeared during absence proof", retainedID)
	}
	return nil
}

func verifyRolledBackLinkAbsent(ctx context.Context, runner CommandRunner, rollback txstate.RollbackMetadata) error {
	needsLinkProof := len(rollback.TUN) != 0 || len(rollback.TUNAddresses) != 0 || len(rollback.DNS) != 0
	if !needsLinkProof {
		return nil
	}
	for _, address := range rollback.TUNAddresses {
		if !ownedRollbackMetadata(address.Owner, netexecutor.OwnerTunAddress) ||
			address.InterfaceName != managedInterface || address.LinkIndex <= 0 ||
			address.LinkKind != "tun" || !address.AppearedAfterCore ||
			strings.TrimSpace(address.CIDR) == "" {
			return errors.New("retained TUN address identity is incomplete or ambiguous")
		}
	}
	for _, tun := range rollback.TUN {
		if !ownedRollbackMetadata(tun.Owner, netexecutor.OwnerTunDevice) || tun.InterfaceName != managedInterface {
			return errors.New("retained TUN link identity is incomplete or ambiguous")
		}
	}
	result, err := runReadOnlyAbsenceCommand(ctx, runner, "ip", "-details", "-o", "link", "show", "dev", managedInterface)
	if resourceMissing(result) {
		return nil
	}
	if commandSucceeded(result, err) {
		return errors.New("transaction-bound TUN link remains present after completed rollback")
	}
	return fmt.Errorf("cannot prove transaction-bound TUN link absence: %s", commandFailureMessage(result, err))
}

func verifyRolledBackRoutesAbsent(ctx context.Context, runner CommandRunner, routes []txstate.RouteRollback) error {
	for _, route := range routes {
		if !ownedRollbackMetadata(route.Owner, netexecutor.OwnerRoute) || strings.TrimSpace(route.Table) == "" || strings.TrimSpace(route.CIDR) == "" {
			return errors.New("retained route identity is incomplete or ambiguous")
		}
		result, err := runReadOnlyAbsenceCommand(ctx, runner, "ip", "-4", "route", "show", "table", strings.TrimSpace(route.Table), strings.TrimSpace(route.CIDR))
		if !commandSucceeded(result, err) {
			return fmt.Errorf("cannot prove exact route absence: %s", commandFailureMessage(result, err))
		}
		if strings.TrimSpace(result.Stdout) != "" {
			return fmt.Errorf("exact route remains present: %s table %s", route.CIDR, route.Table)
		}
	}
	return nil
}

func verifyRolledBackPolicyRulesAbsent(ctx context.Context, runner CommandRunner, rules []txstate.PolicyRuleRollback) error {
	for _, rule := range rules {
		if !ownedRollbackMetadata(rule.Owner, netexecutor.OwnerPolicyRule) || rule.Priority <= 0 || strings.TrimSpace(rule.Table) == "" {
			return errors.New("retained policy-rule identity is incomplete or ambiguous")
		}
		result, err := runReadOnlyAbsenceCommand(ctx, runner, "ip", "-4", "rule", "show", "priority", strconv.Itoa(rule.Priority))
		if !commandSucceeded(result, err) {
			return fmt.Errorf("cannot prove exact policy-rule absence: %s", commandFailureMessage(result, err))
		}
		if strings.TrimSpace(result.Stdout) != "" {
			return fmt.Errorf("policy-rule priority %d remains occupied after rollback", rule.Priority)
		}
	}
	return nil
}

func verifyRolledBackDNSAbsent(ctx context.Context, runner CommandRunner, entries []txstate.DNSRollback) error {
	for _, dns := range entries {
		if !ownedRollbackMetadata(dns.Owner, netexecutor.OwnerDNS) || dns.Link != managedInterface || !systemdResolvedBackend(dns.Backend) {
			return errors.New("retained DNS identity is incomplete or ambiguous")
		}
		result, err := runReadOnlyAbsenceCommand(ctx, runner, "resolvectl", "status", managedInterface, "--no-pager")
		if observeResolvedLink(ctx, result, err) != resolvedLinkAbsent {
			return errors.New("systemd-resolved mutation absence is not conclusively proven")
		}
	}
	return nil
}

func verifyRolledBackNFTablesAbsent(ctx context.Context, runner CommandRunner, entries []txstate.NFTablesRollback) error {
	seen := make(map[string]struct{})
	for _, nft := range entries {
		if !ownedRollbackMetadata(nft.Owner, netexecutor.OwnerFirewall) || !isManagedNFTTarget(nft.Family, nft.Table) {
			return errors.New("retained nftables identity is incomplete or ambiguous")
		}
		key := strings.TrimSpace(nft.Family) + " " + strings.TrimSpace(nft.Table)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result, err := runReadOnlyAbsenceCommand(ctx, runner, "nft", "list", "table", strings.TrimSpace(nft.Family), strings.TrimSpace(nft.Table))
		if resourceMissing(result) {
			continue
		}
		if commandSucceeded(result, err) {
			return fmt.Errorf("exact nftables table remains present: %s", key)
		}
		return fmt.Errorf("cannot prove nftables table absence: %s", commandFailureMessage(result, err))
	}
	return nil
}

func verifyRolledBackChildProcessesAbsent(tx txstate.Transaction, readFile func(string) ([]byte, error)) error {
	generated := make(map[string]struct{}, len(tx.Rollback.GeneratedConfigs))
	for _, cfg := range tx.Rollback.GeneratedConfigs {
		if cfg.Owner == txstate.TransactionOwner && strings.TrimSpace(cfg.Path) != "" {
			generated[filepath.Clean(cfg.Path)] = struct{}{}
		}
	}
	for _, child := range tx.Rollback.ChildProcesses {
		configRef := filepath.Clean(strings.TrimSpace(child.ConfigRef))
		startTime := strings.TrimSpace(child.StartTime)
		if child.Owner != txstate.TransactionOwner || child.PID <= 1 || child.Label != "xray" || configRef == "." || startTime == "" {
			return errors.New("retained tracked child identity is incomplete or ambiguous")
		}
		if _, ok := generated[configRef]; !ok || filepath.Clean(tx.DesiredPlan.Core.RuntimeConfigPath) != configRef || tx.DesiredPlan.Core.Owner != txstate.TransactionOwner || tx.DesiredPlan.Core.ProcessLabel != "xray" {
			return errors.New("retained tracked child config identity is inconsistent")
		}
		data, err := readFile(fmt.Sprintf("/proc/%d/stat", child.PID))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect tracked child pid %d start time: %w", child.PID, err)
		}
		currentStart, err := processStartTimeFromProcStat(child.PID, data)
		if err != nil {
			return err
		}
		if currentStart != startTime {
			// The PID now names another process. It is evidence that the tracked
			// original child is absent, never authority over the replacement.
			continue
		}
		return fmt.Errorf("tracked child pid %d with exact start time remains present after completed rollback", child.PID)
	}
	return nil
}

func processStartTimeFromProcStat(pid int, data []byte) (string, error) {
	text := string(data)
	closeParen := strings.LastIndex(text, ")")
	if closeParen < 0 || closeParen+2 >= len(text) {
		return "", fmt.Errorf("parse tracked child pid %d stat: malformed comm field", pid)
	}
	fields := strings.Fields(text[closeParen+2:])
	const startTimeIndex = 22 - 3
	if len(fields) <= startTimeIndex {
		return "", fmt.Errorf("parse tracked child pid %d stat: missing start time", pid)
	}
	start := strings.TrimSpace(fields[startTimeIndex])
	if start == "" {
		return "", fmt.Errorf("parse tracked child pid %d stat: empty start time", pid)
	}
	return start, nil
}

func verifyRolledBackGeneratedConfigsAbsent(runtimeDir string, tx txstate.Transaction, pathExists func(string) (bool, error)) error {
	generatedDir := filepath.Join(runtimeDir, generatedDirName)
	for _, config := range tx.Rollback.GeneratedConfigs {
		path := filepath.Clean(config.Path)
		if config.Owner != txstate.TransactionOwner || !isUnderDir(generatedDir, path) {
			return errors.New("retained generated-config identity is incomplete or ambiguous")
		}
		present, err := pathExists(path)
		if err != nil {
			return fmt.Errorf("inspect generated runtime config %s: %w", path, err)
		}
		if present {
			return fmt.Errorf("generated runtime config remains present: %s", path)
		}
	}
	return nil
}

func runReadOnlyAbsenceCommand(ctx context.Context, runner CommandRunner, command string, args ...string) (CommandResult, error) {
	path, err := runner.LookPath(command)
	if err != nil {
		return CommandResult{ExitCode: -1}, fmt.Errorf("%s command is unavailable: %w", command, err)
	}
	return runCommand(ctx, runner, path, args...)
}
