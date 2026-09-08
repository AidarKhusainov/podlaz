package recovery

import (
	"context"
	"errors"
	"fmt"
	"strings"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

// exactNftablesRollbackPlan reconstructs the composition that was durably
// persisted alongside the transaction rollback tuple. The rollback tuple grants
// cleanup authority; desired state supplies the exact composition that must be
// re-observed before that authority may mutate the live table.
func exactNftablesRollbackPlan(tx txstate.Transaction, entry txstate.NFTablesRollback) (planner.TunFirewallPlan, error) {
	if !ownedRollbackMetadata(entry.Owner, netexecutor.OwnerFirewall) || !isManagedNFTTarget(entry.Family, entry.Table) {
		return planner.TunFirewallPlan{}, errors.New("nftables rollback target is not exact Podlaz authority")
	}

	matches := 0
	for _, persisted := range tx.Rollback.NFTables {
		if ownedRollbackMetadata(persisted.Owner, netexecutor.OwnerFirewall) &&
			strings.TrimSpace(persisted.Family) == strings.TrimSpace(entry.Family) &&
			strings.TrimSpace(persisted.Table) == strings.TrimSpace(entry.Table) {
			matches++
		}
	}
	if matches != 1 {
		return planner.TunFirewallPlan{}, fmt.Errorf("nftables rollback identity cardinality=%d, want 1", matches)
	}

	desired := tx.DesiredPlan.NFT
	if !ownedRollbackMetadata(desired.Owner, netexecutor.OwnerFirewall) ||
		strings.TrimSpace(desired.Family) != strings.TrimSpace(entry.Family) ||
		strings.TrimSpace(desired.Table) != strings.TrimSpace(entry.Table) {
		return planner.TunFirewallPlan{}, errors.New("exact persisted nftables desired identity does not match rollback authority")
	}
	if len(desired.Chains) == 0 {
		return planner.TunFirewallPlan{}, errors.New("exact persisted nftables composition has no chains")
	}
	if len(desired.Chains) > 1 {
		for _, chain := range desired.Chains {
			if len(chain.Rules) != 0 {
				return planner.TunFirewallPlan{}, fmt.Errorf("exact persisted nftables rule-to-chain mapping is ambiguous across %d chains", len(desired.Chains))
			}
		}
	}

	plan := planner.TunFirewallPlan{
		Backend:     planner.FirewallBackendNftables,
		Family:      strings.TrimSpace(desired.Family),
		Table:       strings.TrimSpace(desired.Table),
		TableAction: planner.FirewallTableAction,
	}
	seenChains := make(map[string]struct{}, len(desired.Chains))
	for _, persisted := range desired.Chains {
		name := strings.TrimSpace(persisted.Name)
		if name == "" || strings.TrimSpace(persisted.Type) == "" || strings.TrimSpace(persisted.Hook) == "" || strings.TrimSpace(persisted.Policy) == "" {
			return planner.TunFirewallPlan{}, errors.New("exact persisted nftables chain metadata is incomplete")
		}
		if persisted.Owner != "" && !ownedRollbackMetadata(persisted.Owner, netexecutor.OwnerFirewall) {
			return planner.TunFirewallPlan{}, fmt.Errorf("exact persisted nftables chain %s has unsupported owner", name)
		}
		if _, duplicate := seenChains[name]; duplicate {
			return planner.TunFirewallPlan{}, fmt.Errorf("exact persisted nftables chain %s is duplicated", name)
		}
		seenChains[name] = struct{}{}
		plan.Chains = append(plan.Chains, planner.TunFirewallChainPlan{
			Name:     name,
			Type:     strings.TrimSpace(persisted.Type),
			Hook:     strings.TrimSpace(persisted.Hook),
			Priority: persisted.Priority,
			Policy:   strings.TrimSpace(persisted.Policy),
			Action:   planner.FirewallTableAction,
		})
		for _, rawRule := range persisted.Rules {
			rule, err := exactPersistedNftablesRule(plan.Family, plan.Table, name, rawRule)
			if err != nil {
				return planner.TunFirewallPlan{}, err
			}
			plan.Rules = append(plan.Rules, rule)
		}
	}
	return plan, nil
}

func exactPersistedNftablesRule(family, table, chain, raw string) (planner.TunFirewallRulePlan, error) {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) < 4 || fields[len(fields)-2] != "owner" {
		return planner.TunFirewallRulePlan{}, fmt.Errorf("exact persisted nftables rule on %s has no ownership marker", chain)
	}
	ownership := fields[len(fields)-1]
	const ownershipPrefix = "podlaz:firewall:"
	if !strings.HasPrefix(ownership, ownershipPrefix) || strings.TrimPrefix(ownership, ownershipPrefix) == "" {
		return planner.TunFirewallRulePlan{}, fmt.Errorf("exact persisted nftables rule on %s has unsupported owner %q", chain, ownership)
	}
	verdictIndex := len(fields) - 3
	if verdictIndex <= 0 {
		return planner.TunFirewallRulePlan{}, fmt.Errorf("exact persisted nftables rule on %s has no expression", chain)
	}
	verdict := fields[verdictIndex]
	switch verdict {
	case planner.FirewallVerdictAccept, planner.FirewallVerdictReject, planner.FirewallVerdictDrop:
	default:
		return planner.TunFirewallRulePlan{}, fmt.Errorf("exact persisted nftables rule on %s has unsupported verdict %q", chain, verdict)
	}
	expr := strings.Join(fields[:verdictIndex], " ")
	ownerKey := strings.TrimPrefix(ownership, ownershipPrefix)
	return planner.TunFirewallRulePlan{
		Chain:       chain,
		Expr:        expr,
		Verdict:     verdict,
		Action:      planner.FirewallActionAdd,
		Ownership:   ownership,
		RollbackKey: strings.Join([]string{family, table, chain, ownerKey}, "/"),
	}, nil
}

type recoveryNftablesMutationRunner interface {
	NftablesGeneration(context.Context) (uint32, error)
	NftablesRemoveTable(context.Context, string, string, uint64, uint32) error
	NftablesReplaceTable(context.Context, string, string, uint64, uint32, planner.TunFirewallPlan) error
}

type nftablesExecutorRunner struct{ runner CommandRunner }

func (r nftablesExecutorRunner) Run(ctx context.Context, name string, args ...string) (netexecutor.CommandResult, error) {
	result, err := r.runner.Run(ctx, name, args...)
	return netexecutor.CommandResult{
		Stdout: result.Stdout, Stderr: result.Stderr,
		RawStdout: result.RawStdout, RawStderr: result.RawStderr,
		ExitCode: result.ExitCode,
	}, err
}

type nftablesExecutorMutationRunner struct {
	nftablesExecutorRunner
	mutation recoveryNftablesMutationRunner
}

func (r nftablesExecutorMutationRunner) NftablesGeneration(ctx context.Context) (uint32, error) {
	return r.mutation.NftablesGeneration(ctx)
}

func (r nftablesExecutorMutationRunner) NftablesRemoveTable(ctx context.Context, family, table string, handle uint64, generation uint32) error {
	return r.mutation.NftablesRemoveTable(ctx, family, table, handle, generation)
}

func (r nftablesExecutorMutationRunner) NftablesReplaceTable(ctx context.Context, family, table string, handle uint64, generation uint32, plan planner.TunFirewallPlan) error {
	return r.mutation.NftablesReplaceTable(ctx, family, table, handle, generation, plan)
}

func nftablesExecutorCommandRunner(runner CommandRunner) netexecutor.CommandRunner {
	if runner == nil {
		runner = OSRunner{}
	}
	base := nftablesExecutorRunner{runner: runner}
	if mutation, ok := runner.(recoveryNftablesMutationRunner); ok {
		return nftablesExecutorMutationRunner{nftablesExecutorRunner: base, mutation: mutation}
	}
	return base
}
