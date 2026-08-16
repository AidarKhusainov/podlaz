package daemon

import (
	"context"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/doctor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
)

type doctorLifecycleSnapshot struct {
	state       xrayState
	coreRunning bool
}

type doctorMutationState struct {
	generation uint64
	pending    bool
}

func (m *XrayManager) captureDoctorLifecycleSnapshot() doctorLifecycleSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state
	if state.Connection == "" {
		state = inactiveXrayState()
	}
	state.Warnings = append([]string(nil), state.Warnings...)
	return doctorLifecycleSnapshot{state: state, coreRunning: m.cmd != nil}
}

func (m *XrayManager) doctorFromSnapshot(ctx context.Context, snapshot doctorLifecycleSnapshot) api.DoctorResponse {
	runtimeDir := m.runtimeDir()
	report := doctor.RunWithOptions(ctx, doctor.Options{
		RuntimeDir:              runtimeDir,
		RuntimeDirOwnedByDaemon: true,
		Lifecycle:               lifecycleDiagnosticContext(runtimeDir, snapshot.state),
	})
	report = doctor.WithSource(report, doctor.SourceDaemon)
	report = doctor.WithDaemonCheck(report, doctor.SeverityOK, "running")
	report.Checks = append(report.Checks, m.lifecycleDoctorChecksFromSnapshot(ctx, snapshot)...)
	return doctor.ToDaemon(report)
}

func (m *XrayManager) lifecycleDoctorChecksFromSnapshot(ctx context.Context, snapshot doctorLifecycleSnapshot) []doctor.Check {
	state := snapshot.state
	coreSeverity := doctor.SeverityOK
	coreMessage := "inactive"
	switch {
	case state.Connection == "error (core exited)":
		coreSeverity = doctor.SeverityFail
		coreMessage = "core exited unexpectedly; inspect podlaz logs --core"
	case snapshot.coreRunning:
		coreMessage = emptyAs(state.Proxy, "core process is running")
	case state.Connection == "active":
		coreSeverity = doctor.SeverityWarning
		coreMessage = "connection is active but no supervised Xray process is registered"
	}

	checks := []doctor.Check{{Name: "core", Severity: coreSeverity, Message: coreMessage}}
	if state.Mode != planner.ModeTun && state.TransactionID == "" {
		return checks
	}

	hostSnapshot := m.collectTunSnapshot(ctx, tunSnapshotOptionsForState(state))
	checks = append(checks,
		tunDoctorCheck(state, hostSnapshot),
		routeDoctorCheck(state, hostSnapshot),
		dnsDoctorCheck(state, hostSnapshot),
		firewallDoctorCheck(state, hostSnapshot),
	)
	if state.TransactionID != "" {
		checks = append(checks, transactionDoctorCheck(m.runtimeDir(), state.TransactionID))
	}
	return checks
}

func (l *lifecycleOperationLock) doctorMutationSnapshot() doctorMutationState {
	if l == nil {
		return doctorMutationState{}
	}
	l.mutationMu.Lock()
	defer l.mutationMu.Unlock()
	return doctorMutationState{
		generation: l.mutationGeneration,
		pending:    l.pendingMutations > 0,
	}
}

func doctorPublicationLifecycleStable(before, after doctorMutationState, initial, current doctorLifecycleSnapshot, status api.StatusResponse) bool {
	if before.pending || after.pending || before.generation != after.generation {
		return false
	}
	if !doctorLifecycleSnapshotsMatch(initial, current) {
		return false
	}
	return doctorSnapshotMatchesStatus(initial, status)
}

func doctorLifecycleSnapshotsMatch(left, right doctorLifecycleSnapshot) bool {
	leftState := left.state
	rightState := right.state
	return left.coreRunning == right.coreRunning &&
		leftState.Connection == rightState.Connection &&
		leftState.Mode == rightState.Mode &&
		leftState.ProfileID == rightState.ProfileID &&
		leftState.ProfileName == rightState.ProfileName &&
		leftState.Proxy == rightState.Proxy &&
		leftState.TUN == rightState.TUN &&
		leftState.Routes == rightState.Routes &&
		leftState.DNS == rightState.DNS &&
		leftState.Firewall == rightState.Firewall &&
		leftState.RuntimeConfigPath == rightState.RuntimeConfigPath &&
		leftState.TransactionID == rightState.TransactionID
}

func doctorSnapshotMatchesStatus(snapshot doctorLifecycleSnapshot, status api.StatusResponse) bool {
	state := snapshot.state
	if status.Connection != state.Connection ||
		status.Mode != state.Mode ||
		status.ProfileID != state.ProfileID ||
		status.ProfileName != state.ProfileName ||
		status.RuntimeConfigPath != state.RuntimeConfigPath ||
		status.Proxy != state.Proxy ||
		status.TUN != state.TUN ||
		status.Routes != state.Routes ||
		status.DNS != state.DNS ||
		status.Firewall != state.Firewall {
		return false
	}
	if state.Connection == "active" && state.Mode == planner.ModeTun {
		return strings.TrimSpace(state.TransactionID) != "" && status.ActiveTransactionID == state.TransactionID
	}
	return status.ActiveTransactionID == ""
}

func withIncompleteDoctorLifecycle(response api.DoctorResponse, scan recovery.PlanResult) api.DoctorResponse {
	response.Checks = append(response.Checks, api.DoctorCheck{
		Name:     "lifecycle-consistency",
		Severity: string(doctor.SeverityWarning),
		Message:  "lifecycle changed during diagnostic inspection; this report is incomplete, rerun podlaz doctor",
	})
	incomplete := cloneRecoveryPlan(scan)
	incomplete.Warnings = append(incomplete.Warnings, recovery.Warning{
		Target:  "lifecycle snapshot",
		Message: "lifecycle changed during diagnostic inspection",
	})
	// Empty status deliberately prevents either active or inactive clean wording;
	// the appended warning forces startup-scan publication to incomplete/stale-incomplete.
	return withStartupScanDoctor(response, incomplete, api.StatusResponse{})
}
