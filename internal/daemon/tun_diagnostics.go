package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

const (
	tunDiagnosticRunTimeout        = 40 * time.Second
	tunFailureDiagnosticRunTimeout = 6 * time.Second
	tunDiagnosticFinalizeTimeout   = 2 * time.Second
)

type tunDiagnosticInput struct {
	state          xrayState
	coreRunning    bool
	plan           planner.TunPlan
	snapshot       netsnapshot.Snapshot
	serverEndpoint string
	serverName     string
	metadataError  string
	failurePhase   string
	rollbackStatus string
}

func (m *XrayManager) TunDiagnostics(ctx context.Context) tundiag.Report {
	m.mu.Lock()
	state := m.state
	coreRunning := m.cmd != nil
	m.mu.Unlock()

	store := tundiag.Store{RuntimeDir: m.runtimeDir()}
	if state.Connection != "active" || state.Mode != planner.ModeTun {
		if report, _, err := store.Load(); err == nil {
			return report
		}
		return tundiag.Finalize(tundiag.Report{
			GeneratedAt: time.Now().UTC(),
			Session: tundiag.Session{
				State: state.Connection,
				Mode:  state.Mode,
			},
			Probes: []tundiag.ProbeResult{{
				ID:             "session",
				Layer:          tundiag.LayerSession,
				Status:         tundiag.ProbeFail,
				Classification: tundiag.ClassSessionInactive,
				Error:          "no active podlaz TUN session and no saved TUN diagnostic report",
			}},
		})
	}

	transaction, _, err := (txstate.TransactionStore{RuntimeDir: m.runtimeDir()}).Load(state.TransactionID)
	if err != nil {
		return tundiag.Finalize(tundiag.Report{
			GeneratedAt: time.Now().UTC(),
			Session:     tunDiagnosticSession(state, coreRunning),
			Probes: []tundiag.ProbeResult{{
				ID:             "session",
				Layer:          tundiag.LayerSession,
				Status:         tundiag.ProbeFail,
				Classification: tundiag.ClassSessionMetadataInconsistent,
				Error:          fmt.Sprintf("load active TUN transaction: %v", err),
			}},
		})
	}
	plan := tunPlanFromTransaction(transaction)
	plan.ProfileName = state.ProfileName
	if plan.TunDevice.Name == "" {
		plan.TunDevice.Name = netsnapshot.DefaultTunName
	}
	serverEndpoint, serverName := tunDiagnosticServerMetadata(transaction)
	snapshot := m.collectTunSnapshot(ctx, tunSnapshotOptionsForState(state))
	return m.runAndPersistTunDiagnostics(ctx, tunDiagnosticInput{
		state: state, coreRunning: coreRunning, plan: plan, snapshot: snapshot,
		serverEndpoint: serverEndpoint, serverName: serverName,
	})
}

func (m *XrayManager) runAndPersistTunDiagnostics(ctx context.Context, input tunDiagnosticInput) tundiag.Report {
	runCtx, cancel := context.WithTimeout(ctx, tunDiagnosticRunTimeout)
	defer cancel()
	report := tundiag.Runner{}.Run(runCtx, tunDiagnosticBase(input), tundiag.StandardProbes(buildPhaseAwareTunDiagnosticAdapters(input)))
	if input.metadataError != "" {
		report = appendTunInternalDiagnosticFailure(report, "transaction-metadata", input.metadataError)
	}
	report, _ = m.persistTunDiagnosticReport(report)
	return report
}

func (m *XrayManager) runAndPersistTunFailureDiagnostics(ctx context.Context, input tunDiagnosticInput, cause error) (tundiag.Report, bool) {
	report := tundiag.Runner{}.Run(ctx, tunDiagnosticBase(input), tundiag.PreRollbackProbes(buildPhaseAwareTunDiagnosticAdapters(input)))
	if input.metadataError != "" {
		report = appendTunInternalDiagnosticFailure(report, "transaction-metadata", input.metadataError)
	}
	report = appendTunLifecycleFailureProbe(report, input.failurePhase, cause)
	if cause != nil {
		report.Errors = append(report.Errors, "TUN lifecycle failure: "+cause.Error())
	}
	return m.persistTunDiagnosticReport(tundiag.Finalize(report))
}

func (m *XrayManager) persistTunDiagnosticReport(report tundiag.Report) (tundiag.Report, bool) {
	path, err := (tundiag.Store{RuntimeDir: m.runtimeDir()}).Save(report)
	if err != nil {
		report.ReportPath = ""
		report = appendTunInternalDiagnosticFailure(report, "report-persistence", "persist latest TUN diagnostic report: "+err.Error())
		return tundiag.Finalize(report), false
	}
	report.ReportPath = path
	return tundiag.Finalize(report), true
}

func appendTunInternalDiagnosticFailure(report tundiag.Report, id, message string) tundiag.Report {
	if _, exists := report.Probe(id); exists {
		return report
	}
	report.Probes = append(report.Probes, tundiag.ProbeResult{
		ID:             id,
		Layer:          tundiag.LayerSession,
		Status:         tundiag.ProbeFail,
		Classification: tundiag.ClassInternalDiagnosticError,
		Error:          message,
	})
	return report
}

func (m *XrayManager) collectTunFailureDiagnostics(ctx context.Context, transactionID string, plan planner.TunPlan, cause error) tunFailureDiagnosticSummary {
	state := xrayState{
		Connection:    "verifying",
		Mode:          planner.ModeTun,
		ProfileID:     plan.ProfileID,
		ProfileName:   plan.ProfileName,
		TUN:           "enabled (" + emptyAs(plan.TunDevice.Name, netsnapshot.DefaultTunName) + ")",
		TransactionID: transactionID,
	}
	input := tunDiagnosticInput{
		state:          state,
		coreRunning:    true,
		plan:           plan,
		failurePhase:   tunLifecycleDiagnosticFailurePhase(cause),
		rollbackStatus: "pending",
	}
	if transaction, _, err := (txstate.TransactionStore{RuntimeDir: m.runtimeDir()}).Load(transactionID); err != nil {
		input.metadataError = "load TUN transaction diagnostic metadata: " + err.Error()
	} else {
		input.serverEndpoint, input.serverName = tunDiagnosticServerMetadata(transaction)
	}
	diagnosticCtx, cancel := context.WithTimeout(ctx, tunFailureDiagnosticRunTimeout)
	defer cancel()
	input.snapshot = m.collectTunSnapshot(diagnosticCtx, tunSnapshotOptionsForState(state))
	report, persisted := m.runAndPersistTunFailureDiagnostics(diagnosticCtx, input, cause)
	classification := report.PrimaryClassification
	if classification == "" {
		classification = tundiag.ClassInternalDiagnosticError
	}
	return tunFailureDiagnosticSummary{
		PrimaryClassification: classification,
		ReportPath:            report.ReportPath,
		Persisted:             persisted,
	}
}

func tunLifecycleDiagnosticFailurePhase(cause error) string {
	var mutationErr *tunNetworkMutationError
	if errors.As(cause, &mutationErr) && strings.TrimSpace(mutationErr.Phase()) != "" {
		return mutationErr.Phase()
	}
	return "connectivity-verify"
}

func (m *XrayManager) finalizeTunFailureDiagnosticRollback(ctx context.Context, summary tunFailureDiagnosticSummary, status string) {
	if !summary.Persisted || strings.TrimSpace(summary.ReportPath) == "" {
		return
	}
	finalizeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), tunDiagnosticFinalizeTimeout)
	defer cancel()
	select {
	case <-finalizeCtx.Done():
		return
	default:
	}
	store := tundiag.Store{RuntimeDir: m.runtimeDir()}
	report, _, err := store.Load()
	if err != nil {
		return
	}
	report.RollbackStatus = strings.TrimSpace(status)
	_, _ = store.Save(report)
}

func tunDiagnosticBase(input tunDiagnosticInput) tundiag.Report {
	plan := input.plan
	snapshot := input.snapshot
	bypass := tunDiagnosticServerBypass(plan)
	serverAddresses := []string{}
	serverEndpoint := strings.TrimSpace(input.serverEndpoint)
	if bypass.Destination != "" {
		serverAddress := strings.TrimSuffix(bypass.Destination, "/32")
		serverAddresses = append(serverAddresses, serverAddress)
		if serverEndpoint == "" {
			serverEndpoint = serverAddress
		}
	}
	tunName := emptyAs(plan.TunDevice.Name, netsnapshot.DefaultTunName)
	return tundiag.Report{
		GeneratedAt:    time.Now().UTC(),
		FailurePhase:   strings.TrimSpace(input.failurePhase),
		RollbackStatus: strings.TrimSpace(input.rollbackStatus),
		Session:        tunDiagnosticSession(input.state, input.coreRunning),
		Network: tundiag.Network{
			PhysicalInterface: snapshot.DefaultIPv4.Interface,
			SSID:              tunDiagnosticConnectionName(snapshot),
			Gateway:           snapshot.DefaultIPv4.Gateway,
			LocalAddresses:    tunDiagnosticLocalAddresses(snapshot.DefaultIPv4.Interface),
			TunInterface:      tunName,
			TunMTU:            firstPositive(plan.TunDevice.MTU, readInterfaceMTU(tunName)),
			UplinkMTU:         readInterfaceMTU(snapshot.DefaultIPv4.Interface),
			DNSServers:        append([]string(nil), plan.DNS.Servers...),
			IPv4Status:        string(snapshot.IPv4.Status),
			IPv6Status:        string(snapshot.IPv6.Status),
			ServerEndpoint:    serverEndpoint,
			ServerHostname:    tunDiagnosticHostname(serverEndpoint),
			ServerName:        strings.TrimSpace(input.serverName),
			ServerAddresses:   serverAddresses,
			DoHProviders:      tunDiagnosticDoHEndpoints(),
		},
	}
}

func tunDiagnosticSession(state xrayState, coreRunning bool) tundiag.Session {
	return tundiag.Session{
		State:          emptyAs(state.Connection, "inactive"),
		Mode:           state.Mode,
		ProfileName:    state.ProfileName,
		TransactionID:  state.TransactionID,
		CoreRunning:    coreRunning,
		Interface:      netsnapshot.DefaultTunName,
		MetadataSource: "podlazd active state and transaction",
	}
}

func tunDiagnosticServerBypass(plan planner.TunPlan) planner.TunRoutePlan {
	if plan.ServerBypass.Destination != "" {
		return plan.ServerBypass
	}
	for _, route := range plan.Routes {
		if route.Table == planner.MainRoutingTable && route.Destination != "" && route.Destination != planner.IPv4DefaultRoute {
			return route
		}
	}
	return planner.TunRoutePlan{}
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
