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
		ctx.Interface = doctor.ManagedResourceExactOwned
		ctx.InterfaceLinkIndex = plan.TunAddress.LinkIndex
		ctx.InterfaceLinkKind = plan.TunAddress.LinkKind
	}

	if strings.TrimSpace(plan.Firewall.Table) == "" && strings.TrimSpace(plan.Firewall.Family) == "" {
		ctx.NFTTable = doctor.ManagedResourceExpectedAbsent
	} else if plan.Firewall.Family == netsnapshot.DefaultNFTFamily && plan.Firewall.Table == netsnapshot.DefaultNFTTable {
		ctx.NFTTable = doctor.ManagedResourceExactOwned
	}
	return ctx
}
