package executor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestRollbackResourceScopedClassifiesOnlyExactMissingLinkProtocol(t *testing.T) {
	tests := []struct {
		name     string
		result   CommandResult
		err      error
		missing  bool
		contains string
	}{
		{
			name: "valid missing device LF",
			result: CommandResult{
				ExitCode:  1,
				RawStderr: "Device \"podlaz0\" does not exist.\n",
				Stderr:    "Device \"podlaz0\" does not exist.",
			},
			err:     executorTestExitError{code: 1},
			missing: true,
		},
		{
			name: "valid cannot find device CRLF",
			result: CommandResult{
				ExitCode:  1,
				RawStderr: "Cannot find device \"podlaz0\"\r\n",
				Stderr:    "Cannot find device \"podlaz0\"",
			},
			err:     executorTestExitError{code: 1},
			missing: true,
		},
		{
			name: "unterminated stderr",
			result: CommandResult{
				ExitCode:  1,
				RawStderr: "Device \"podlaz0\" does not exist.",
				Stderr:    "Device \"podlaz0\" does not exist.",
			},
			err: executorTestExitError{code: 1},
		},
		{
			name: "extra line",
			result: CommandResult{
				ExitCode:  1,
				RawStderr: "Device \"podlaz0\" does not exist.\nextra\n",
				Stderr:    "Device \"podlaz0\" does not exist.\nextra",
			},
			err: executorTestExitError{code: 1},
		},
		{
			name: "leading whitespace",
			result: CommandResult{
				ExitCode:  1,
				RawStderr: " Device \"podlaz0\" does not exist.\n",
				Stderr:    "Device \"podlaz0\" does not exist.",
			},
			err: executorTestExitError{code: 1},
		},
		{
			name: "normalized only without raw stderr",
			result: CommandResult{
				ExitCode: 1,
				Stderr:   "Device \"podlaz0\" does not exist.",
			},
			err: executorTestExitError{code: 1},
		},
		{
			name: "nil error with exit code",
			result: CommandResult{
				ExitCode:  1,
				RawStderr: "Device \"podlaz0\" does not exist.\n",
				Stderr:    "Device \"podlaz0\" does not exist.",
			},
		},
		{
			name: "arbitrary non-exit error",
			result: CommandResult{
				ExitCode:  1,
				RawStderr: "Device \"podlaz0\" does not exist.\n",
				Stderr:    "Device \"podlaz0\" does not exist.",
			},
			err: errors.New("synthetic runner failure"),
		},
		{
			name: "context cancellation",
			result: CommandResult{
				ExitCode:  1,
				RawStderr: "Device \"podlaz0\" does not exist.\n",
				Stderr:    "Device \"podlaz0\" does not exist.",
			},
			err: context.Canceled,
		},
		{
			name: "deadline exceeded",
			result: CommandResult{
				ExitCode:  1,
				RawStderr: "Device \"podlaz0\" does not exist.\n",
				Stderr:    "Device \"podlaz0\" does not exist.",
			},
			err: context.DeadlineExceeded,
		},
		{
			name: "launch error",
			result: CommandResult{
				ExitCode:  -1,
				RawStderr: "fork/exec ip: no such file or directory",
				Stderr:    "fork/exec ip: no such file or directory",
			},
			err:      errors.New("fork/exec ip: no such file or directory"),
			contains: "no such file or directory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := rollbackResourceScopedWithLinkResult(tt.result, tt.err)
			if tt.missing {
				if err == nil || !IsTunRollbackLinkAbsent(err) {
					t.Fatalf("expected typed missing-link result, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected fail-closed rollback identity error")
			}
			if IsTunRollbackLinkAbsent(err) {
				t.Fatalf("ambiguous result was misclassified as proven missing link: %v", err)
			}
			if tt.contains != "" && !strings.Contains(err.Error(), tt.contains) {
				t.Fatalf("expected error to contain %q, got %v", tt.contains, err)
			}
		})
	}
}

func TestRollbackResourceScopedDoesNotTreatOperationalNoSuchFileAsMissingLink(t *testing.T) {
	msg := "fork/exec ip: no such file or directory"
	err := rollbackResourceScopedWithLinkResult(
		CommandResult{ExitCode: -1, Stderr: msg, RawStderr: msg},
		errors.New(msg),
	)
	if err == nil {
		t.Fatal("expected rollback identity inspection failure")
	}
	if IsTunRollbackLinkAbsent(err) {
		t.Fatalf("operational no such file error was misclassified as proven missing link: %v", err)
	}
	if !strings.Contains(err.Error(), "no such file or directory") {
		t.Fatalf("expected original operational error to be preserved, got %v", err)
	}
}

func rollbackResourceScopedWithLinkResult(result CommandResult, err error) error {
	recorder := &callRecorder{}
	exec := DNSAwareTunExecutor{
		Base: TunExecutor{
			TunDevice:   fakeTun{rec: recorder},
			TunAddress:  IPTunAddressExecutor{Runner: singleResultRunner{result: result, err: err}},
			Routes:      fakeRoutes{rec: recorder},
			PolicyRules: fakeRules{rec: recorder},
		},
	}
	plan := planner.TunPlan{
		TunDevice:  planner.TunDevicePlan{Name: managedTunInterfaceName, Action: "verify"},
		TunAddress: rollbackIdentityAddressPlanForTest(),
	}
	return exec.RollbackResourceScoped(context.Background(), plan)
}

type singleResultRunner struct {
	result CommandResult
	err    error
}

func (r singleResultRunner) Run(context.Context, string, ...string) (CommandResult, error) {
	return r.result, r.err
}
