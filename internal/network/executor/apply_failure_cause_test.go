package executor

import (
	"context"
	"errors"
	"os/exec"
	"testing"
)

func TestApplyFailureCauseUsesTypedCommandEvidence(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "non-zero exit",
			err: commandError{
				name:   "ip",
				result: CommandResult{ExitCode: 2},
				err:    errors.New("exit status 2"),
			},
			want: ApplyFailureCauseCommandExit,
		},
		{
			name: "bounded command timeout",
			err: commandError{
				name:       "resolvectl",
				result:     CommandResult{ExitCode: -1},
				err:        context.DeadlineExceeded,
				contextErr: context.DeadlineExceeded,
			},
			want: ApplyFailureCauseCommandTimeout,
		},
		{
			name: "command unavailable",
			err: commandError{
				name:   "resolvectl",
				result: CommandResult{ExitCode: -1},
				err:    &exec.Error{Name: "resolvectl", Err: exec.ErrNotFound},
			},
			want: ApplyFailureCauseCommandUnavailable,
		},
		{
			name: "unknown typed cause",
			err:  errors.New("opaque executor failure"),
			want: ApplyFailureCauseUnknown,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := withApplyFailureSubphase(applyFailureSubphaseDNS, tc.err)
			if got := ApplyFailureCause(err); got != tc.want {
				t.Fatalf("ApplyFailureCause()=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestApplyFailureCauseDoesNotUseParentCancellationAsCommandCause(t *testing.T) {
	err := commandError{
		name:       "ip",
		result:     CommandResult{ExitCode: -1},
		err:        context.Canceled,
		parentErr:  context.Canceled,
		contextErr: context.Canceled,
	}
	if got := ApplyFailureCause(err); got != ApplyFailureCauseUnknown {
		t.Fatalf("parent cancellation cause=%q, want unknown so lifecycle classification remains authoritative", got)
	}
}
