package doctor

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/render"
)

const (
	defaultRuntimeDir = "/run/podlaz"
	managedInterface  = "podlaz0"
	managedNFTTable   = "inet podlaz"
)

const (
	SourceDaemon        = "daemon"
	SourceLocalFallback = "local fallback"
)

// Severity describes the outcome of a diagnostic check.
type Severity string

const (
	SeverityOK      Severity = "OK"
	SeverityWarning Severity = "WARN"
	SeverityFail    Severity = "FAIL"
)

// Check is a single diagnostic result.
type Check struct {
	Name     string
	Severity Severity
	Message  string
}

// Report contains all diagnostic checks for the current host.
type Report struct {
	Source string
	Checks []Check
}

// Options controls doctor execution. Zero values use safe production defaults.
type Options struct {
	Runner                  CommandRunner
	RuntimeDir              string
	RuntimeDirOwnedByDaemon bool
}

// Run executes safe diagnostics. It must not mutate system state.
func Run(ctx context.Context) Report {
	return RunWithOptions(ctx, Options{})
}

// RunWithOptions executes safe diagnostics with injectable dependencies for tests.
func RunWithOptions(ctx context.Context, opts Options) Report {
	runner := opts.Runner
	if runner == nil {
		runner = OSRunner{}
	}

	runtimeDir := opts.RuntimeDir
	if runtimeDir == "" {
		runtimeDir = defaultRuntimeDir
	}

	checks := []Check{{
		Name:     "platform",
		Severity: platformSeverity(),
		Message:  fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}}

	ipPath, ipOK := commandAvailability(runner, "ip", "iproute2")
	checks = append(checks, ipPath.check)

	route := defaultRoute(ctx, runner, ipPath.path, ipOK)
	checks = append(checks, route.routeCheck, route.interfaceCheck)

	nmcliPath, _ := commandAvailability(runner, "nmcli", "networkmanager")
	checks = append(checks, nmcliPath.check)

	systemctlPath, _ := commandAvailability(runner, "systemctl", "systemd")
	checks = append(checks, systemctlPath.check)

	resolvectlPath, resolvectlOK := commandAvailability(runner, "resolvectl", "resolved")
	if resolvectlOK {
		resolvectlPath.check.Message += "; " + resolvedDNSDiagnosticLine(ctx, runner, resolvectlPath.path)
	}
	checks = append(checks, resolvectlPath.check)

	nftPath, nftOK := commandAvailability(runner, "nft", "nftables")
	checks = append(checks, nftPath.check)

	checks = append(checks, staleResources(ctx, runner, staleResourceOptions{
		ipPath:                  ipPath.path,
		ipOK:                    ipOK,
		nftPath:                 nftPath.path,
		nftOK:                   nftOK,
		runtimeDir:              runtimeDir,
		runtimeDirOwnedByDaemon: opts.RuntimeDirOwnedByDaemon,
	}))

	return Report{Source: SourceLocalFallback, Checks: checks}
}

// FromDaemon converts a validated daemon API response into the user-facing doctor report.
func FromDaemon(d api.DoctorResponse) Report {
	checks := make([]Check, 0, len(d.Checks))
	for _, check := range d.Checks {
		checks = append(checks, Check{
			Name:     check.Name,
			Severity: Severity(check.Severity),
			Message:  check.Message,
		})
	}
	return Report{Source: d.Source, Checks: checks}
}

// ToDaemon converts a doctor report into the daemon API response contract.
func ToDaemon(r Report) api.DoctorResponse {
	checks := make([]api.DoctorCheck, 0, len(r.Checks))
	for _, check := range r.Checks {
		checks = append(checks, api.DoctorCheck{
			Name:     check.Name,
			Severity: string(check.Severity),
			Message:  check.Message,
		})
	}
	return api.DoctorResponse{Source: r.normalizedSource(), Checks: checks}
}

// WithSource returns a copy of the report with a specific source label.
func WithSource(r Report, source string) Report {
	r.Source = source
	return r
}

// WithDaemonCheck prepends daemon reachability information to the report.
func WithDaemonCheck(r Report, severity Severity, message string) Report {
	checks := make([]Check, 0, len(r.Checks)+1)
	checks = append(checks, Check{Name: "daemon", Severity: severity, Message: message})
	checks = append(checks, r.Checks...)
	r.Checks = checks
	return r
}

// HasFailures reports whether any diagnostic check failed.
func (r Report) HasFailures() bool {
	for _, check := range r.Checks {
		if check.Severity == SeverityFail {
			return true
		}
	}
	return false
}

// String renders the report in a stable, CLI-friendly format.
func (r Report) String() string {
	var b strings.Builder
	b.WriteString("podlaz doctor report\n")
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
