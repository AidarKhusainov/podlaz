package daemon

import (
	"context"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func maybeRecordE2EDNSRollback(executor tunPlanExecutor) tunPlanExecutor {
	switch e2eTunHookPhase() {
	case e2eTunHookDNSApplyPhase, e2eTunHookDNSMissingLinkRollbackPhase:
		dnsAware, ok := executor.(netexecutor.DNSAwareTunExecutor)
		if !ok || dnsAware.DNS == nil {
			return executor
		}
		dnsAware.DNS = e2eDNSRollbackEventExecutor{delegate: dnsAware.DNS}
		return dnsAware
	default:
		return executor
	}
}

type e2eDNSRollbackEventExecutor struct {
	delegate netexecutor.DNSExecutor
}

func (e e2eDNSRollbackEventExecutor) Apply(ctx context.Context, plan planner.TunDNSPlan) (netexecutor.Step, error) {
	return e.delegate.Apply(ctx, plan)
}

func (e e2eDNSRollbackEventExecutor) Verify(ctx context.Context, plan planner.TunDNSPlan) error {
	return e.delegate.Verify(ctx, plan)
}

func (e e2eDNSRollbackEventExecutor) Rollback(ctx context.Context, plan planner.TunDNSPlan) error {
	recordE2ETunHookEvent("dns-rollback-started")
	return e.delegate.Rollback(ctx, plan)
}
