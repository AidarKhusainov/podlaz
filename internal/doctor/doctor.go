package doctor

import (
	"context"
	"fmt"
	"runtime"
	"strings"
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

// Run executes safe diagnostics. It must not mutate system state.
func Run(_ context.Context) Report {
	checks := []Check{
		{
			Name:     "platform",
			Severity: platformSeverity(),
			Message:  fmt.Sprintf("GOOS=%s GOARCH=%s", runtime.GOOS, runtime.GOARCH),
		},
		{
			Name:     "network-mutations",
			Severity: SeverityWarning,
			Message:  "network mutation checks are not implemented yet; this build is read-only",
		},
		{
			Name:     "panic-reset",
			Severity: SeverityOK,
			Message:  "emergency reset plan is available in dry-run mode",
		},
	}

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
