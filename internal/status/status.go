package status

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AidarKhusainov/tunwarden/internal/api"
	"github.com/AidarKhusainov/tunwarden/internal/render"
)

const generatedDirName = "generated"

// RuntimeDirectoryState describes local TunWarden runtime directory visibility.
type RuntimeDirectoryState string

const (
	RuntimeDirectoryMissing RuntimeDirectoryState = "missing"
	RuntimeDirectoryPresent RuntimeDirectoryState = "present"
	RuntimeDirectoryPath    RuntimeDirectoryState = "path"
	RuntimeDirectoryUnknown RuntimeDirectoryState = "unknown"
)

// RuntimeDirectory is the read-only filesystem status for TunWarden runtime state.
type RuntimeDirectory struct {
	Path    string
	State   RuntimeDirectoryState
	Message string
}

// Candidate describes stale TunWarden-owned local runtime state that recover can inspect.
type Candidate struct {
	Kind        string
	Description string
	Target      string
}

// Warning describes local status visibility that could not be completed.
type Warning struct {
	Target  string
	Message string
}

// Report is the local status snapshot rendered by the v0.1 CLI fallback.
type Report struct {
	Daemon           string
	Connection       string
	RuntimeDirectory RuntimeDirectory
	Proxy            string
	TUN              string
	Candidates       []Candidate
	Warnings         []Warning
}

// Options controls local status inspection. Zero values use safe production defaults.
type Options struct {
	RuntimeDir string
}

// Inspect returns the current local TunWarden status without requiring daemon IPC.
func Inspect(ctx context.Context) Report {
	return InspectWithOptions(ctx, Options{})
}

// InspectWithOptions returns local TunWarden status with injectable paths for tests.
func InspectWithOptions(ctx context.Context, opts Options) Report {
	runtimeDir := opts.RuntimeDir
	if runtimeDir == "" {
		runtimeDir = api.RuntimeDirFromEnv()
	}

	report := Report{
		Daemon: "not running",
		RuntimeDirectory: RuntimeDirectory{
			Path: runtimeDir,
		},
		Proxy: "inactive",
		TUN:   "not managed in this build",
	}

	select {
	case <-ctx.Done():
		report.Connection = "unknown (inspection incomplete)"
		report.RuntimeDirectory.State = RuntimeDirectoryUnknown
		report.RuntimeDirectory.Message = "unknown (inspection incomplete)"
		report.Warnings = append(report.Warnings, Warning{
			Target:  "runtime directory " + runtimeDir,
			Message: ctx.Err().Error(),
		})
		return report
	default:
	}

	runtime, runtimeWarning := inspectRuntimeDirectory(runtimeDir)
	report.RuntimeDirectory = runtime
	if runtimeWarning != nil {
		report.Warnings = append(report.Warnings, *runtimeWarning)
	}

	generatedCandidates, generatedWarning := inspectGeneratedRuntimeConfigs(filepath.Join(runtimeDir, generatedDirName))
	report.Candidates = append(report.Candidates, generatedCandidates...)
	if generatedWarning != nil {
		report.Warnings = append(report.Warnings, *generatedWarning)
	}
	report.Candidates = append(report.Candidates, runtimeCandidate(runtime)...)
	report.Connection = connectionState(report.Candidates, report.Warnings)
	return report
}

// FromDaemon converts a daemon API response into the user-facing status report.
func FromDaemon(s api.StatusResponse) Report {
	return Report{
		Daemon:     valueOrDefault(s.Daemon, "running"),
		Connection: valueOrDefault(s.Connection, "inactive"),
		RuntimeDirectory: RuntimeDirectory{
			State:   RuntimeDirectoryPresent,
			Message: valueOrDefault(s.RuntimeDirectory, "present"),
		},
		Proxy:    valueOrDefault(s.Proxy, "inactive"),
		TUN:      valueOrDefault(s.TUN, "disabled"),
		Warnings: warningsFromStrings("daemon", s.Warnings),
	}
}

// WithDaemonUnavailable annotates a local fallback report with actionable daemon availability guidance.
func WithDaemonUnavailable(base Report, message string) Report {
	base.Daemon = "not reachable (" + message + "); using local fallback"
	return base
}

// HasUnhealthyState reports whether status found stale state or incomplete visibility.
func (r Report) HasUnhealthyState() bool {
	return len(r.Candidates) > 0 || len(r.Warnings) > 0
}

// String renders the status report in a stable, CLI-friendly format.
func (r Report) String() string {
	var b strings.Builder
	b.WriteString("TunWarden status\n")
	fmt.Fprintf(&b, "Daemon: %s\n", render.Redact(r.Daemon))
	fmt.Fprintf(&b, "Connection: %s\n", render.Redact(r.Connection))
	fmt.Fprintf(&b, "Runtime directory: %s\n", render.Redact(r.RuntimeDirectory.Message))
	fmt.Fprintf(&b, "Proxy: %s\n", render.Redact(r.Proxy))
	fmt.Fprintf(&b, "TUN: %s\n", render.Redact(r.TUN))
	fmt.Fprintf(&b, "Stale state: %s\n", render.Redact(staleStateLine(r.Candidates, r.Warnings)))

	if len(r.Candidates) > 0 {
		b.WriteString("Recovery candidates:\n")
		for _, candidate := range r.Candidates {
			fmt.Fprintf(&b, "  - %s: %s\n", render.Redact(candidate.Description), render.Redact(candidate.Target))
		}
	}

	if len(r.Warnings) > 0 {
		b.WriteString("Inspection warnings:\n")
		for _, warning := range r.Warnings {
			fmt.Fprintf(&b, "  - could not inspect %s: %s\n", render.Redact(warning.Target), render.Redact(warning.Message))
		}
	}

	switch {
	case len(r.Candidates) > 0 && len(r.Warnings) > 0:
		b.WriteString("Guidance: run `tunwarden recover` for the canonical read-only recovery dry-run and `tunwarden doctor` for diagnostic detail.\n")
	case len(r.Candidates) > 0:
		b.WriteString("Guidance: run `tunwarden recover` for the canonical read-only recovery dry-run.\n")
	case len(r.Warnings) > 0:
		b.WriteString("Guidance: run `tunwarden doctor` for diagnostic detail.\n")
	}

	return b.String()
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
		return runtime, &Warning{
			Target:  "runtime directory " + runtimeDir,
			Message: err.Error(),
		}
	}
}

func inspectGeneratedRuntimeConfigs(generatedDir string) ([]Candidate, *Warning) {
	stat, err := os.Stat(generatedDir)
	switch {
	case err == nil && stat.IsDir():
		return []Candidate{{
			Kind:        "generated-runtime-configs",
			Description: "generated runtime configs",
			Target:      generatedDir,
		}}, nil
	case err == nil:
		return []Candidate{{
			Kind:        "generated-runtime-configs",
			Description: "generated runtime config path",
			Target:      generatedDir,
		}}, nil
	case errors.Is(err, os.ErrNotExist):
		return nil, nil
	default:
		return nil, &Warning{
			Target:  "generated runtime configs " + generatedDir,
			Message: err.Error(),
		}
	}
}

func runtimeCandidate(runtime RuntimeDirectory) []Candidate {
	switch runtime.State {
	case RuntimeDirectoryPresent:
		return []Candidate{{
			Kind:        "runtime-directory",
			Description: "runtime directory",
			Target:      runtime.Path,
		}}
	case RuntimeDirectoryPath:
		return []Candidate{{
			Kind:        "runtime-directory",
			Description: "runtime path",
			Target:      runtime.Path,
		}}
	default:
		return nil
	}
}

func connectionState(candidates []Candidate, warnings []Warning) string {
	switch {
	case len(candidates) > 0:
		return "inactive (stale state detected)"
	case len(warnings) > 0:
		return "unknown (inspection incomplete)"
	default:
		return "inactive"
	}
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

func candidateNoun(count int) string {
	if count == 1 {
		return "candidate"
	}
	return "candidates"
}

func valueOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func warningsFromStrings(target string, warnings []string) []Warning {
	out := make([]Warning, 0, len(warnings))
	for _, warning := range warnings {
		out = append(out, Warning{Target: target, Message: warning})
	}
	return out
}
