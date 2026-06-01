package doctor

import (
	"context"
	"fmt"
	"runtime"
	"strings"
)

const (
	defaultRuntimeDir = "/run/tunwarden"
	managedInterface  = "tunwarden0"
	managedNFTTable   = "inet tunwarden"
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
	Checks []Check
}

// Options controls doctor execution. Zero values use safe production defaults.
type Options struct {
	Runner     CommandRunner
	RuntimeDir string
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

	resolvectlPath, _ := commandAvailability(runner, "resolvectl", "resolved")
	checks = append(checks, resolvectlPath.check)

	nftPath, nftOK := commandAvailability(runner, "nft", "nftables")
	checks = append(checks, nftPath.check)

	checks = append(checks, staleResources(ctx, runner, staleResourceOptions{
		ipPath:     ipPath.path,
		ipOK:       ipOK,
		nftPath:    nftPath.path,
		nftOK:      nftOK,
		runtimeDir: runtimeDir,
	}))

	return Report{Checks: checks}
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
	b.WriteString("TunWarden doctor report\n")
	for _, check := range r.Checks {
		fmt.Fprintf(&b, "[%s] %s: %s\n", check.Severity, check.Name, check.Message)
	}
	return b.String()
}

func platformSeverity() Severity {
	if runtime.GOOS == "linux" {
		return SeverityOK
	}
	return SeverityWarning
}
