package daemon

import (
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/doctor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func lifecycleDiagnosticContext(runtimeDir string, state xrayState) doctor.LifecycleDiagnosticContext {
	switch {
	case state.Connection != "active":
		return doctor.LifecycleDiagnosticContext{
			State:     doctor.LifecycleInactive,
			Interface: doctor.ManagedResourceExpectedAbsent,
			NFTTable:  doctor.ManagedResourceExpectedAbsent,
		}
	case state.Mode != planner.ModeTun:
		return doctor.LifecycleDiagnosticContext{
			State:     doctor.LifecycleActiveProxy,
			Interface: doctor.ManagedResourceExpectedAbsent,
			NFTTable:  doctor.ManagedResourceExpectedAbsent,
		}
	}

	ctx := doctor.LifecycleDiagnosticContext{
		State:         doctor.LifecycleActiveTUN,
		TransactionID: strings.TrimSpace(state.TransactionID),
		Interface:     doctor.ManagedResourceUnproven,
		NFTTable:      doctor.ManagedResourceUnproven,
	}
	if ctx.TransactionID == "" {
		return ctx
	}

	tx, _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Load(ctx.TransactionID)
	if err != nil {
		return ctx
	}
	ctx.TransactionState = tx.State
	ctx.TransactionRequiresCleanup = tx.RequiresCleanup()
	if tx.ID != ctx.TransactionID || tx.State != txstate.TransactionCommitted || tx.RequiresCleanup() || tx.Mode != planner.ModeTun {
		return ctx
	}
	if state.ProfileID != "" && tx.ProfileID != state.ProfileID {
		return ctx
	}
	if err := validateTunRollbackProjection(tx); err != nil {
		return ctx
	}

	plan := tunPlanFromTransaction(tx)
	if plan.TunAddress.Interface == "podlaz0" && plan.TunAddress.LinkIndex > 0 && plan.TunAddress.LinkKind == "tun" {
		ctx.Interface = doctor.ManagedResourceExpectedOwned
		ctx.InterfaceLinkIndex = plan.TunAddress.LinkIndex
		ctx.InterfaceLinkKind = plan.TunAddress.LinkKind
	}

	if transactionExpectsNoNFTables(tx) {
		ctx.NFTTable = doctor.ManagedResourceExpectedAbsent
		return ctx
	}
	firewallPlan, err := tunRevalidationFirewallPlan(tx)
	if err != nil {
		return ctx
	}
	if firewallPlan.Family != netsnapshot.DefaultNFTFamily || firewallPlan.Table != netsnapshot.DefaultNFTTable {
		return ctx
	}
	ctx.NFTTable = doctor.ManagedResourceExpectedOwned
	ctx.NFTPlan = &firewallPlan
	return ctx
}

func transactionExpectsNoNFTables(tx txstate.Transaction) bool {
	nft := tx.DesiredPlan.NFT
	return len(tx.Rollback.NFTables) == 0 &&
		strings.TrimSpace(nft.Family) == "" &&
		strings.TrimSpace(nft.Table) == "" &&
		strings.TrimSpace(nft.Owner) == "" &&
		len(nft.Chains) == 0
}
