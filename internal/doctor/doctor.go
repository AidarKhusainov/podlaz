package doctor

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/AidarKhusainov/tunwarden/internal/api"
	"github.com/AidarKhusainov/tunwarden/internal/render"
	txstate "github.com/AidarKhusainov/tunwarden/internal/state"
)

const (
	defaultRuntimeDir = "/run/tunwarden"
	managedInterface  = "tunwarden0"
	managedNFTTable   = "inet tunwarden"
)

const (
	SourceDaemon        = "daemon"
	SourceLocalFallback = "local fallback"
)

type Severity string

const (
	SeverityOK      Severity = "OK"
	SeverityWarning Severity = "WARN"
	SeverityFail    Severity = "FAIL"
)

type Check struct {
	Name     string
	Severity Severity
	Message  string
}

type Report struct {
	Source string
	Checks []Check
}

type Options struct {
	Runner                  CommandRunner
	RuntimeDir              string
	RuntimeDirOwnedByDaemon bool
}

func Run(ctx context.Context) Report { return RunWithOptions(ctx, Options{}) }

func RunWithOptions(ctx context.Context, opts Options) Report {
	runner := opts.Runner
	if runner == nil {
		runner = OSRunner{}
	}

	runtimeDir := opts.RuntimeDir
	if runtimeDir == "" {
		runtimeDir = defaultRuntimeDir
	}

	checks := []Check{{Name: "platform", Severity: platformSeverity(), Message: fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)}}

	ipPath, ipOK := commandAvailability(runner, "ip", "iproute2")
	checks = append(checks, ipPath.check)

	route := defaultRoute(ctx, runner, ipPath.path, ipOK)
	checks = append(checks, route.routeCheck, route.interfaceCheck)

	nmcliPath, _ := commandAvailability(runner, "nmcli", "networkmanager")
	checks = append(checks, nmcliPath.check)

	systemctlPath, _ := commandAvailability(runner, "systemctl", "systemd")
	checks = append(checks, systemctlPath.check)

	resolvectlPath, _ := commandAvailability(runner, "resolvectl", "resolved")
	checks = append(checks, resolvectlPath.check)

	nftPath, nftOK := commandAvailability(runner, "nft", "nftables")
	checks = append(checks, nftPath.check)

	checks = append(checks, staleResources(ctx, runner, staleResourceOptions{ipPath: ipPath.path, ipOK: ipOK, nftPath: nftPath.path, nftOK: nftOK, runtimeDir: runtimeDir, runtimeDirOwnedByDaemon: opts.RuntimeDirOwnedByDaemon}))
	checks = append(checks, transactionStateCheck(runtimeDir))

	return Report{Source: SourceLocalFallback, Checks: checks}
}

func transactionStateCheck(runtimeDir string) Check {
	summaries, warnings := txstate.ScanTransactions(runtimeDir)
	parts := make([]string, 0, len(summaries)+len(warnings))
	for _, summary := range summaries {
		if !summary.RequiresCleanup {
			continue
		}
		parts = append(parts, fmt.Sprintf("transaction %s %s; rollback available: %s; state path: %s", summary.ID, summary.StatusLine(), summary.RollbackLine(), summary.Path))
	}
	for _, warning := range warnings {
		parts = append(parts, "cannot inspect transaction state: "+warning)
	}
	if len(parts) == 0 {
		return Check{Name: "transactions", Severity: SeverityOK, Message: "no pending transaction state found"}
	}
	return Check{Name: "transactions", Severity: SeverityWarning, Message: strings.Join(parts, "; ")}
}

func FromDaemon(d api.DoctorResponse) Report {
	checks := make([]Check, 0, len(d.Checks))
	for _, check := range d.Checks {
		checks = append(checks, Check{Name: check.Name, Severity: Severity(check.Severity), Message: check.Message})
	}
	return Report{Source: d.Source, Checks: checks}
}

func ToDaemon(r Report) api.DoctorResponse {
	checks := make([]api.DoctorCheck, 0, len(r.Checks))
	for _, check := range r.Checks {
		checks = append(checks, api.DoctorCheck{Name: check.Name, Severity: string(check.Severity), Message: check.Message})
	}
	return api.DoctorResponse{Source: r.normalizedSource(), Checks: checks}
}

func WithSource(r Report, source string) Report { r.Source = source; return r }

func WithDaemonCheck(r Report, severity Severity, message string) Report {
	checks := make([]Check, 0, len(r.Checks)+1)
	checks = append(checks, Check{Name: "daemon", Severity: severity, Message: message})
	checks = append(checks, r.Checks...)
	r.Checks = checks
	return r
}

func (r Report) HasFailures() bool {
	for _, check := range r.Checks {
		if check.Severity == SeverityFail {
			return true
		}
	}
	return false
}

func (r Report) String() string {
	var b strings.Builder
	b.WriteString("TunWarden doctor report\n")
	fmt.Fprintf(&b, "Source: %s\n", render.Redact(r.normalizedSource()))
	for _, check := range r.Checks {
		fmt.Fprintf(&b, "[%s] %s: %s\n", check.Severity, render.Redact(check.Name), render.Redact(check.Message))
	}
	return b.String()
}

func (r Report) normalizedSource() string {
	if r.Source != "" {
		return r.Source
	}
	return SourceLocalFallback
}

func platformSeverity() Severity {
	if runtime.GOOS == "linux" {
		return SeverityOK
	}
	return SeverityWarning
}
