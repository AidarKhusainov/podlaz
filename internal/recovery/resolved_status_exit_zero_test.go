package recovery

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestObserveResolvedLinkAcceptsExactExitZeroMissingDeviceStatus(t *testing.T) {
	for _, tc := range []struct {
		name       string
		terminator string
	}{
		{name: "LF", terminator: "\n"},
		{name: "CRLF", terminator: "\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := exitZeroMissingResolvedStatus(tc.terminator)

			if got := observeResolvedLink(context.Background(), result, nil); got != resolvedLinkAbsent {
				t.Fatalf("exact exit-0 missing-device status must be authoritative absence, got %v", got)
			}
		})
	}
}

func TestObserveResolvedLinkExitZeroMissingDeviceStatusFailsClosed(t *testing.T) {
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	deadlineCtx, deadlineCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	t.Cleanup(deadlineCancel)
	<-deadlineCtx.Done()

	exact := exitZeroMissingResolvedStatus("\n")
	tests := []struct {
		name   string
		ctx    context.Context
		result CommandResult
		err    error
	}{
		{
			name: "non-empty raw stdout",
			ctx:  context.Background(),
			result: CommandResult{
				Stdout:    "unexpected",
				RawStdout: "unexpected\n",
				Stderr:    exact.Stderr,
				RawStderr: exact.RawStderr,
				ExitCode:  0,
			},
		},
		{
			name: "unterminated stderr",
			ctx:  context.Background(),
			result: CommandResult{
				Stderr:    resolvedMissingDeviceIgnoringStderr,
				RawStderr: resolvedMissingDeviceIgnoringStderr,
				ExitCode:  0,
			},
		},
		{
			name: "extra blank stderr line",
			ctx:  context.Background(),
			result: CommandResult{
				Stderr:    resolvedMissingDeviceIgnoringStderr,
				RawStderr: resolvedMissingDeviceIgnoringStderr + "\n\n",
				ExitCode:  0,
			},
		},
		{
			name: "additional stderr line",
			ctx:  context.Background(),
			result: CommandResult{
				Stderr:    resolvedMissingDeviceIgnoringStderr + "\nunexpected",
				RawStderr: resolvedMissingDeviceIgnoringStderr + "\nunexpected\n",
				ExitCode:  0,
			},
		},
		{
			name: "superset message",
			ctx:  context.Background(),
			result: CommandResult{
				Stderr:    resolvedMissingDeviceIgnoringStderr + " (cached)",
				RawStderr: resolvedMissingDeviceIgnoringStderr + " (cached)\n",
				ExitCode:  0,
			},
		},
		{
			name: "arbitrary stderr",
			ctx:  context.Background(),
			result: CommandResult{
				Stderr:    "temporary resolver warning",
				RawStderr: "temporary resolver warning\n",
				ExitCode:  0,
			},
		},
		{
			name: "older missing-device line is not inferred for exit zero",
			ctx:  context.Background(),
			result: CommandResult{
				Stderr:    resolvedMissingDeviceStderr,
				RawStderr: resolvedMissingDeviceStderr + "\n",
				ExitCode:  0,
			},
		},
		{name: "nil context", ctx: nil, result: exact},
		{name: "cancelled context", ctx: cancelledCtx, result: exact},
		{name: "deadline exceeded context", ctx: deadlineCtx, result: exact},
		{
			name:   "launch failure",
			ctx:    context.Background(),
			result: CommandResult{ExitCode: -1},
			err:    errors.New("fork/exec resolvectl: executable file not found"),
		},
		{
			name:   "signal termination",
			ctx:    context.Background(),
			result: CommandResult{ExitCode: -1},
			err:    errors.New("signal: killed"),
		},
		{
			name: "unexpected nonzero exit status",
			ctx:  context.Background(),
			result: CommandResult{
				Stderr:    resolvedMissingDeviceIgnoringStderr,
				RawStderr: resolvedMissingDeviceIgnoringStderr + "\n",
				ExitCode:  2,
			},
			err: issue243ExitError{code: 2},
		},
		{
			name: "oversized stderr",
			ctx:  context.Background(),
			result: CommandResult{
				Stderr:    strings.Repeat("x", maxResolvedMissingStderrSize+1),
				RawStderr: strings.Repeat("x", maxResolvedMissingStderrSize+1),
				ExitCode:  0,
			},
		},
		{
			name:   "error despite zero exit code",
			ctx:    context.Background(),
			result: exact,
			err:    errors.New("unexpected runner error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := observeResolvedLink(tt.ctx, tt.result, tt.err); got != resolvedLinkUnknown {
				t.Fatalf("ambiguous exit-0 status must fail closed as unknown, got %v", got)
			}
		})
	}
}

func TestPlanWithOptionsTreatsExactExitZeroMissingDeviceStatusAsClean(t *testing.T) {
	runner := newResolvedRecoveryRunner(
		[]resolvedRecoveryCommand{missingPodlazLink()},
		[]resolvedRecoveryCommand{{
			stderr: resolvedMissingDeviceIgnoringStderr + "\n",
		}},
	)

	plan := PlanWithOptions(context.Background(), Options{
		Runner:     runner,
		RuntimeDir: filepath.Join(t.TempDir(), "podlaz"),
	})

	if len(plan.Candidates) != 0 || len(plan.Warnings) != 0 {
		t.Fatalf("exact exit-0 missing-device status must produce a clean recovery scan, got %#v", plan)
	}
	if !strings.Contains(plan.String(), "No podlaz-owned recovery candidates found.") {
		t.Fatalf("clean recovery scan must render no candidates, got %q", plan.String())
	}
}

func TestResolvedMissingDeviceMutationContractRejectsExitZeroStatusEnvelope(t *testing.T) {
	result := exitZeroMissingResolvedStatus("\n")

	if resolvedMissingDeviceResult(context.Background(), result, nil) {
		t.Fatal("read-only exit-0 status evidence must not broaden resolvectl revert idempotence")
	}
}

func exitZeroMissingResolvedStatus(terminator string) CommandResult {
	return CommandResult{
		Stderr:    resolvedMissingDeviceIgnoringStderr,
		RawStderr: resolvedMissingDeviceIgnoringStderr + terminator,
		ExitCode:  0,
	}
}

type issue243ExitError struct {
	code int
}

func (e issue243ExitError) Error() string {
	return "exit status"
}

func (e issue243ExitError) ExitCode() int {
	return e.code
}
