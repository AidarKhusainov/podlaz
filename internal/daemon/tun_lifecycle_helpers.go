package daemon

import (
	"context"
	"fmt"

	"github.com/AidarKhusainov/podlaz/internal/doctor"
	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

type tunSnapshotCollector func(context.Context, netsnapshot.Options) netsnapshot.Snapshot

func (m *XrayManager) tunPlanExecutor() tunPlanExecutor {
	if m.tunExecutor != nil {
		return m.tunExecutor
	}
	return netexecutor.NewOSDNSExecutor()
}

func (m *XrayManager) collectTunSnapshot(ctx context.Context, opts netsnapshot.Options) netsnapshot.Snapshot {
	if m.snapshotCollector != nil {
		return m.snapshotCollector(ctx, opts)
	}
	return netsnapshot.Collect(ctx, opts)
}

func tunSnapshotOptionsForState(xrayState) netsnapshot.Options {
	return netsnapshot.Options{}
}

func tunDoctorCheck(state xrayState, snapshot netsnapshot.Snapshot) doctor.Check {
	if state.Mode != planner.ModeTun {
		return doctor.Check{Name: "tun", Severity: doctor.SeverityOK, Message: "disabled"}
	}
	for _, dev := range snapshot.TunDevices {
		if dev.Name == netsnapshot.DefaultTunName && dev.Status == netsnapshot.StatusDetected {
			return doctor.Check{Name: "tun", Severity: doctor.SeverityOK, Message: "podlaz0 detected"}
		}
	}
	return doctor.Check{Name: "tun", Severity: doctor.SeverityWarning, Message: emptyAs(state.TUN, "TUN state is not confirmed by snapshot")}
}

func routeDoctorCheck(state xrayState, snapshot netsnapshot.Snapshot) doctor.Check {
	if state.Mode != planner.ModeTun {
		return doctor.Check{Name: "routes", Severity: doctor.SeverityOK, Message: "not modified"}
	}
	if snapshot.DefaultIPv4.Status != netsnapshot.StatusDetected {
		return doctor.Check{Name: "routes", Severity: doctor.SeverityWarning, Message: "host default route visibility is " + string(snapshot.DefaultIPv4.Status)}
	}
	return doctor.Check{Name: "routes", Severity: doctor.SeverityWarning, Message: "podlaz route table and policy-rule state require transaction/executor verification; host default route alone is not sufficient"}
}

func dnsDoctorCheck(state xrayState, snapshot netsnapshot.Snapshot) doctor.Check {
	if state.Mode != planner.ModeTun {
		return doctor.Check{Name: "dns", Severity: doctor.SeverityOK, Message: "not modified"}
	}
	if snapshot.DNS.Resolved.Status == netsnapshot.StatusDetected {
		return doctor.Check{Name: "dns", Severity: doctor.SeverityOK, Message: emptyAs(state.DNS, "systemd-resolved detected")}
	}
	return doctor.Check{Name: "dns", Severity: doctor.SeverityWarning, Message: "systemd-resolved state is " + string(snapshot.DNS.Resolved.Status)}
}

func firewallDoctorCheck(state xrayState, snapshot netsnapshot.Snapshot) doctor.Check {
	if state.Mode != planner.ModeTun {
		return doctor.Check{Name: "firewall", Severity: doctor.SeverityOK, Message: "not modified"}
	}
	if snapshot.Nftables.PodlazTable.Status == netsnapshot.StatusDetected {
		return doctor.Check{Name: "firewall", Severity: doctor.SeverityOK, Message: "podlaz nftables table detected"}
	}
	if snapshot.Nftables.Availability.Status == netsnapshot.StatusDetected {
		return doctor.Check{Name: "firewall", Severity: doctor.SeverityWarning, Message: emptyAs(state.Firewall, "nftables available; podlaz table not detected in snapshot")}
	}
	return doctor.Check{Name: "firewall", Severity: doctor.SeverityWarning, Message: "nftables state is " + string(snapshot.Nftables.Availability.Status)}
}

func transactionDoctorCheck(runtimeDir, transactionID string) doctor.Check {
	tx, _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Load(transactionID)
	if err != nil {
		return doctor.Check{Name: "transaction", Severity: doctor.SeverityFail, Message: err.Error()}
	}
	severity := doctor.SeverityOK
	if tx.RequiresCleanup() {
		severity = doctor.SeverityWarning
	}
	return doctor.Check{Name: "transaction", Severity: severity, Message: fmt.Sprintf("%s transaction %s", tx.State, tx.ID)}
}
