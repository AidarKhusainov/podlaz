package status

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/render"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

const generatedDirName = "generated"

var lstat = os.Lstat

// RuntimeDirectoryState describes the local podlaz runtime directory state.
type RuntimeDirectoryState string

const (
	RuntimeDirectoryMissing RuntimeDirectoryState = "missing"
	RuntimeDirectoryPresent RuntimeDirectoryState = "present"
	RuntimeDirectoryPath    RuntimeDirectoryState = "path"
	RuntimeDirectoryUnknown RuntimeDirectoryState = "unknown"
)

// RuntimeDirectory is the inspected local runtime directory summary.
type RuntimeDirectory struct {
	Path    string
	State   RuntimeDirectoryState
	Message string
}

// DaemonSocketState describes the local daemon socket path state.
type DaemonSocketState string

const (
	DaemonSocketMissing      DaemonSocketState = "missing"
	DaemonSocketPresent      DaemonSocketState = "present"
	DaemonSocketInaccessible DaemonSocketState = "inaccessible"
	DaemonSocketUnexpected   DaemonSocketState = "unexpected"
	DaemonSocketUnknown      DaemonSocketState = "unknown"
)

// DaemonSocketAccess describes what the caller learned while attempting the daemon API.
type DaemonSocketAccess string

const (
	DaemonSocketAccessUnknown          DaemonSocketAccess = ""
	DaemonSocketAccessPermissionDenied DaemonSocketAccess = "permission-denied"
)

// DaemonSocket is the inspected daemon socket summary used by local fallback status.
type DaemonSocket struct {
	Path    string
	State   DaemonSocketState
	Message string
}

// Candidate describes a local stale-state recovery candidate shown by status.
type Candidate struct {
	Kind        string
	Description string
	Target      string
}

// Warning describes an incomplete local status inspection.
type Warning struct {
	Target  string
	Message string
}

// Report is the user-visible podlaz status model.
type Report struct {
	Daemon            string
	Service           string
	Connection        string
	Mode              string
	ProfileID         string
	ProfileName       string
	DaemonSocket      DaemonSocket
	RuntimeDirectory  RuntimeDirectory
	RuntimeConfigPath string
	Proxy             string
	TUN               string
	Routes            string
	DNS               string
	Firewall          string
	Transactions      []txstate.TransactionSummary
	StartupScan       *api.StartupScanStatus
	Candidates        []Candidate
	Warnings          []Warning
}

// Options controls local status inspection. Zero values use production defaults.
type Options struct {
	RuntimeDir         string
	SocketPath         string
	DaemonSocketAccess DaemonSocketAccess
}

// Inspect returns the local fallback status report using production defaults.
func Inspect(ctx context.Context) Report {
	return InspectWithOptions(ctx, Options{})
}

// InspectWithOptions returns the local fallback status report with injectable options.
func InspectWithOptions(ctx context.Context, opts Options) Report {
	runtimeDir := opts.RuntimeDir
	if runtimeDir == "" {
		runtimeDir = api.RuntimeDirFromEnv()
	}
	socketPath := opts.SocketPath
	if socketPath == "" {
		socketPath = api.SocketPath(runtimeDir)
	}

	report := Report{
		Daemon:  "not running",
		Service: "none",
		DaemonSocket: DaemonSocket{
			Path: socketPath,
		},
		RuntimeDirectory: RuntimeDirectory{
			Path: runtimeDir,
		},
		Proxy: "inactive",
		TUN:   "not managed in this build",
	}

	select {
	case <-ctx.Done():
		report.Connection = "unknown (inspection incomplete)"
		report.DaemonSocket.State = DaemonSocketUnknown
		report.DaemonSocket.Message = "unknown (inspection incomplete)"
		report.RuntimeDirectory.State = RuntimeDirectoryUnknown
		report.RuntimeDirectory.Message = "unknown (inspection incomplete)"
		report.Warnings = append(report.Warnings, Warning{
			Target:  "runtime directory " + runtimeDir,
			Message: ctx.Err().Error(),
		})
		return report
	default:
	}

	socket, socketWarning := inspectDaemonSocket(socketPath, opts.DaemonSocketAccess)
	report.DaemonSocket = socket
	if socketWarning != nil {
		report.Warnings = append(report.Warnings, *socketWarning)
	}
	report.Candidates = append(report.Candidates, daemonSocketCandidate(socket)...)

	deferStaleClassification := deferLocalStaleClassification(socket, opts.DaemonSocketAccess)
	runtime, runtimeWarning := inspectRuntimeDirectory(runtimeDir)
	if deferStaleClassification {
		runtime = runtimeDirectoryWithInaccessibleDaemon(runtime)
	}
	report.RuntimeDirectory = runtime
	if runtimeWarning != nil {
		report.Warnings = append(report.Warnings, *runtimeWarning)
	}

	if deferStaleClassification {
		report.Warnings = append(report.Warnings, Warning{
			Target:  "daemon socket " + socketPath,
			Message: "permission denied; local runtime state may belong to a live podlazd and was not classified as stale",
		})
		report.Connection = connectionState(report.Candidates, report.Warnings)
		return report
	}

	generated, generatedWarning := inspectGeneratedRuntimeConfigs(filepath.Join(runtimeDir, generatedDirName))
	report.Candidates = append(report.Candidates, generated...)
	if generatedWarning != nil {
		report.Warnings = append(report.Warnings, *generatedWarning)
	}

	transactions, transactionCandidates, transactionWarnings := inspectTransactions(runtimeDir)
	report.Transactions = transactions
	report.Candidates = append(report.Candidates, transactionCandidates...)
	report.Warnings = append(report.Warnings, transactionWarnings...)
	report.Candidates = append(report.Candidates, runtimeCandidate(runtime)...)
	report.Connection = connectionState(report.Candidates, report.Warnings)
	return report
}

// FromDaemon converts a daemon API status response into the local status report model.
func FromDaemon(s api.StatusResponse) Report {
	report := Report{
		Daemon:     s.Daemon,
		Service:    s.Service,
		Connection: s.Connection,
		Mode:       s.Mode,
		ProfileID:  s.ProfileID,
		ProfileName: s.ProfileName,
		RuntimeDirectory: RuntimeDirectory{
			State:   RuntimeDirectoryPresent,
			Message: s.RuntimeDirectory,
		},
		RuntimeConfigPath: s.RuntimeConfigPath,
		Proxy:             s.Proxy,
		TUN:               s.TUN,
		Routes:            s.Routes,
		DNS:               s.DNS,
		Firewall:          s.Firewall,
		Transactions:      transactionsFromAPI(s.Transactions),
		Warnings:          warningsFromStrings("daemon", s.Warnings),
	}
	if s.StartupScan != nil {
		scan := *s.StartupScan
		report.StartupScan = &scan
		report.Candidates = append(report.Candidates, candidatesFromAPI(scan.Candidates)...)
		report.Warnings = append(report.Warnings, warningsFromAPI(scan.Warnings)...)
	}
	return report
}

// WithDaemonUnavailable marks a local fallback report with the daemon connection failure.
func WithDaemonUnavailable(base Report, message string) Report {
	base.Daemon = "not reachable (" + message + "); using local fallback"
	return base
}

// HasUnhealthyState reports whether status found recovery candidates, warnings, or cleanup state.
func (r Report) HasUnhealthyState() bool {
	if len(r.Candidates) > 0 || len(r.Warnings) > 0 {
		return true
	}
	if r.StartupScan != nil && (len(r.StartupScan.Candidates) > 0 || len(r.StartupScan.Warnings) > 0) {
		return true
	}
	for _, tx := range r.Transactions {
		if tx.RequiresCleanup {
			return true
		}
	}
	return false
}

// String renders the status report in a stable human-readable format.
func (r Report) String() string {
	var b strings.Builder
	b.WriteString("podlaz status\n")
	fmt.Fprintf(&b, "Daemon: %s\n", render.Redact(r.Daemon))
	fmt.Fprintf(&b, "Service: %s\n", render.Redact(serviceLine(r.Service)))
	fmt.Fprintf(&b, "Connection: %s\n", render.Redact(r.Connection))
	if r.Mode != "" {
		fmt.Fprintf(&b, "Mode: %s\n", render.Redact(r.Mode))
	}
	if r.DaemonSocket.Message != "" {
		fmt.Fprintf(&b, "Daemon socket: %s\n", render.Redact(r.DaemonSocket.Message))
	}
	fmt.Fprintf(&b, "Runtime directory: %s\n", render.Redact(r.RuntimeDirectory.Message))
	if r.RuntimeConfigPath != "" {
		fmt.Fprintf(&b, "Runtime config: %s\n", render.Redact(r.RuntimeConfigPath))
	}
	fmt.Fprintf(&b, "Proxy: %s\n", render.Redact(r.Proxy))
	fmt.Fprintf(&b, "TUN: %s\n", render.Redact(r.TUN))
	if r.Routes != "" {
		fmt.Fprintf(&b, "Routes: %s\n", render.Redact(r.Routes))
	}
	if r.DNS != "" {
		fmt.Fprintf(&b, "DNS: %s\n", render.Redact(r.DNS))
	}
	if r.Firewall != "" {
		fmt.Fprintf(&b, "Firewall: %s\n", render.Redact(r.Firewall))
	}
	fmt.Fprintf(&b, "Stale state: %s\n", render.Redact(staleStateLine(r.Candidates, r.Warnings)))
	if r.StartupScan != nil {
		fmt.Fprintf(&b, "Startup recovery scan: %s\n", render.Redact(startupScanStatusLine(r.StartupScan.Status)))
		if txID := firstStartupTransactionID(r.StartupScan.Candidates); txID != "" {
			fmt.Fprintf(&b, "Pending transaction: %s\n", render.Redact(txID))
		}
		if strings.TrimSpace(r.StartupScan.SuggestedAction) != "" {
			fmt.Fprintf(&b, "Suggested action: %s\n", render.Redact(r.StartupScan.SuggestedAction))
		}
	}
	for _, tx := range r.Transactions {
		fmt.Fprintf(&b, "Transaction: %s\n", render.Redact(tx.StatusLine()))
		fmt.Fprintf(&b, "Rollback available: %s\n", render.Redact(tx.RollbackLine()))
		fmt.Fprintf(&b, "State path: %s\n", render.Redact(tx.Path))
	}
	if len(r.Candidates) > 0 {
		b.WriteString("Recovery candidates:\n")
		for _, c := range r.Candidates {
			fmt.Fprintf(&b, "  - %s: %s\n", render.Redact(c.Description), render.Redact(c.Target))
		}
	}
	if len(r.Warnings) > 0 {
		b.WriteString("Inspection warnings:\n")
		for _, w := range r.Warnings {
			fmt.Fprintf(&b, "  - could not inspect %s: %s\n", render.Redact(w.Target), render.Redact(w.Message))
		}
	}
	switch {
	case len(r.Candidates) > 0 && len(r.Warnings) > 0:
		b.WriteString("Guidance: run `podlaz recover` for the canonical read-only recovery dry-run and `podlaz doctor` for diagnostic detail.\n")
	case len(r.Candidates) > 0:
		b.WriteString("Guidance: run `podlaz recover` for the canonical read-only recovery dry-run.\n")
	case len(r.Warnings) > 0:
		b.WriteString("Guidance: run `podlaz doctor` for diagnostic detail.\n")
	}
	return b.String()
}

func inspectDaemonSocket(socketPath string, access DaemonSocketAccess) (DaemonSocket, *Warning) {
	socket := DaemonSocket{Path: socketPath}
	info, err := lstat(socketPath)
	if access == DaemonSocketAccessPermissionDenied {
		switch {
		case err == nil && info.Mode()&os.ModeSocket != 0:
			socket.State = DaemonSocketInaccessible
			socket.Message = "present but inaccessible (permission denied; check podlaz group membership)"
			return socket, nil
		case err != nil && errors.Is(err, os.ErrPermission):
			socket.State = DaemonSocketInaccessible
			socket.Message = "inaccessible (permission denied; check podlaz group membership)"
			return socket, &Warning{
				Target:  "daemon socket " + socketPath,
				Message: "permission denied while inspecting daemon socket path",
			}
		}
	}

	switch {
	case err == nil && info.Mode()&os.ModeSocket != 0:
		socket.State = DaemonSocketPresent
		socket.Message = "present"
		return socket, nil
	case err == nil:
		socket.State = DaemonSocketUnexpected
		socket.Message = "present as non-socket path (stale)"
		return socket, nil
	case errors.Is(err, os.ErrNotExist):
		socket.State = DaemonSocketMissing
		socket.Message = "missing"
		return socket, nil
	default:
		socket.State = DaemonSocketUnknown
		socket.Message = "unknown (inspection incomplete)"
		return socket, &Warning{Target: "daemon socket " + socketPath, Message: err.Error()}
	}
}

func inspectRuntimeDirectory(runtimeDir string) (RuntimeDirectory, *Warning) {
	runtime := RuntimeDirectory{Path: runtimeDir}
	stat, err := os.Stat(runtimeDir)
	switch {
	case err == nil && stat.IsDir():
		runtime.State = RuntimeDirectoryPresent
		runtime.Message = "present (stale)"
		return runtime, nil
	case err == nil:
		runtime.State = RuntimeDirectoryPath
		runtime.Message = "present as non-directory (stale)"
		return runtime, nil
	case errors.Is(err, os.ErrNotExist):
		runtime.State = RuntimeDirectoryMissing
		runtime.Message = "missing"
		return runtime, nil
	default:
		runtime.State = RuntimeDirectoryUnknown
		runtime.Message = "unknown (inspection incomplete)"
		return runtime, &Warning{Target: "runtime directory " + runtimeDir, Message: err.Error()}
	}
}

func inspectGeneratedRuntimeConfigs(generatedDir string) ([]Candidate, *Warning) {
	stat, err := os.Stat(generatedDir)
	switch {
	case err == nil && stat.IsDir():
		return []Candidate{{Kind: "generated-runtime-configs", Description: "generated runtime configs", Target: generatedDir}}, nil
	case err == nil:
		return []Candidate{{Kind: "generated-runtime-configs", Description: "generated runtime config path", Target: generatedDir}}, nil
	case errors.Is(err, os.ErrNotExist):
		return nil, nil
	default:
		return nil, &Warning{Target: "generated runtime configs " + generatedDir, Message: err.Error()}
	}
}

func inspectTransactions(runtimeDir string) ([]txstate.TransactionSummary, []Candidate, []Warning) {
	summaries, scanWarnings := txstate.ScanTransactions(runtimeDir)
	candidates := make([]Candidate, 0, len(summaries))
	for _, summary := range summaries {
		if summary.RequiresCleanup {
			candidates = append(candidates, Candidate{Kind: "transaction-state", Description: "transaction rollback state", Target: summary.Path})
		}
	}
	return summaries, candidates, warningsFromStrings("transaction state", scanWarnings)
}

func runtimeCandidate(runtime RuntimeDirectory) []Candidate {
	switch runtime.State {
	case RuntimeDirectoryPresent:
		return []Candidate{{Kind: "runtime-directory", Description: "runtime directory", Target: runtime.Path}}
	case RuntimeDirectoryPath:
		return []Candidate{{Kind: "runtime-directory", Description: "runtime path", Target: runtime.Path}}
	default:
		return nil
	}
}

func daemonSocketCandidate(socket DaemonSocket) []Candidate {
	if socket.State != DaemonSocketUnexpected {
		return nil
	}
	return []Candidate{{Kind: "daemon-socket", Description: "daemon socket path", Target: socket.Path}}
}

func deferLocalStaleClassification(socket DaemonSocket, access DaemonSocketAccess) bool {
	return access == DaemonSocketAccessPermissionDenied || socket.State == DaemonSocketInaccessible
}

func runtimeDirectoryWithInaccessibleDaemon(runtime RuntimeDirectory) RuntimeDirectory {
	if runtime.State == RuntimeDirectoryPresent {
		runtime.Message = "present (daemon socket inaccessible; stale status unknown)"
	}
	return runtime
}

func connectionState(candidates []Candidate, warnings []Warning) string {
	if len(candidates) > 0 {
		return "inactive (stale state detected)"
	}
	if len(warnings) > 0 {
		return "unknown (inspection incomplete)"
	}
	return "inactive"
}

func serviceLine(service string) string {
	if service == "" {
		return "unknown"
	}
	return service
}

func staleStateLine(candidates []Candidate, warnings []Warning) string {
	switch {
	case len(candidates) > 0 && len(warnings) > 0:
		return fmt.Sprintf("found %d recovery %s; inspection incomplete", len(candidates), candidateNoun(len(candidates)))
	case len(candidates) > 0:
		return fmt.Sprintf("found %d recovery %s", len(candidates), candidateNoun(len(candidates)))
	case len(warnings) > 0:
		return "unknown (inspection incomplete)"
	default:
		return "none"
	}
}

func startupScanStatusLine(status string) string {
	switch status {
	case api.StartupScanStatusStale:
		return "stale state found"
	case api.StartupScanStatusIncomplete:
		return "inspection incomplete"
	case api.StartupScanStatusStaleIncomplete:
		return "stale state found (inspection incomplete)"
	default:
		return "clean inactive state"
	}
}

func firstStartupTransactionID(candidates []api.RecoveryCandidate) string {
	for _, candidate := range candidates {
		if candidate.Transaction != nil && strings.TrimSpace(candidate.Transaction.ID) != "" {
			return candidate.Transaction.ID
		}
	}
	return ""
}

func candidateNoun(count int) string {
	if count == 1 {
		return "candidate"
	}
	return "candidates"
}

func warningsFromStrings(target string, warnings []string) []Warning {
	out := make([]Warning, 0, len(warnings))
	for _, warning := range warnings {
		out = append(out, Warning{Target: target, Message: warning})
	}
	return out
}

func warningsFromAPI(warnings []api.RecoveryWarning) []Warning {
	out := make([]Warning, 0, len(warnings))
	for _, warning := range warnings {
		out = append(out, Warning{Target: warning.Target, Message: warning.Message})
	}
	return out
}

func candidatesFromAPI(candidates []api.RecoveryCandidate) []Candidate {
	out := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, Candidate{Kind: candidate.Kind, Description: candidate.Description, Target: candidate.Target})
	}
	return out
}

func transactionsFromAPI(in []api.TransactionStatus) []txstate.TransactionSummary {
	out := make([]txstate.TransactionSummary, 0, len(in))
	for _, tx := range in {
		out = append(out, txstate.TransactionSummary{
			ID:                tx.ID,
			State:             txstate.TransactionState(tx.State),
			Path:              tx.Path,
			RollbackAvailable: tx.RollbackAvailable,
			RequiresCleanup:   tx.RequiresCleanup,
		})
	}
	return out
}
