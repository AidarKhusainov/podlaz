package daemon

import (
	"context"
	"errors"
	"testing"
)

func TestNetworkSessionReplayClassificationIsTotalAndConservative(t *testing.T) {
	terminalCause := errors.New("typed terminal replay failure")
	retryableCause := errors.New("typed retryable replay failure")

	tests := []struct {
		name         string
		ctx          context.Context
		err          error
		want         networkSessionReplayDisposition
		wantMutation networkSessionCandidateMutation
	}{
		{
			name:         "explicit terminal",
			ctx:          context.Background(),
			err:          withNetworkSessionReplaySemantics(networkSessionReplayDispositionTerminal, networkSessionCandidateMutationRolledBack, terminalCause),
			want:         networkSessionReplayDispositionTerminal,
			wantMutation: networkSessionCandidateMutationRolledBack,
		},
		{
			name:         "explicit retryable",
			ctx:          context.Background(),
			err:          withNetworkSessionReplaySemantics(networkSessionReplayDispositionRetryable, networkSessionCandidateMutationNotOpened, retryableCause),
			want:         networkSessionReplayDispositionRetryable,
			wantMutation: networkSessionCandidateMutationNotOpened,
		},
		{
			name:         "daemon shutdown",
			ctx:          context.Background(),
			err:          errLifecycleShuttingDown,
			want:         networkSessionReplayDispositionInterrupted,
			wantMutation: networkSessionCandidateMutationUnresolved,
		},
		{
			name:         "unknown wrapped error",
			ctx:          context.Background(),
			err:          errors.Join(errors.New("outer"), errors.New("new unsupported failure")),
			want:         networkSessionReplayDispositionIncomplete,
			wantMutation: networkSessionCandidateMutationUnresolved,
		},
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	tests = append(tests, struct {
		name         string
		ctx          context.Context
		err          error
		want         networkSessionReplayDisposition
		wantMutation networkSessionCandidateMutation
	}{
		name:         "parent cancellation wins",
		ctx:          cancelled,
		err:          withNetworkSessionReplaySemantics(networkSessionReplayDispositionTerminal, networkSessionCandidateMutationRolledBack, terminalCause),
		want:         networkSessionReplayDispositionInterrupted,
		wantMutation: networkSessionCandidateMutationUnresolved,
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, mutation := classifyNetworkSessionReplayFailure(tt.ctx, tt.err)
			if got != tt.want || mutation != tt.wantMutation {
				t.Fatalf("classification=(%q,%q) want=(%q,%q)", got, mutation, tt.want, tt.wantMutation)
			}
		})
	}
}

func TestNetworkSessionReplaySemanticWrapperPreservesCause(t *testing.T) {
	cause := errors.New("typed cause")
	err := withNetworkSessionReplaySemantics(networkSessionReplayDispositionTerminal, networkSessionCandidateMutationNotOpened, cause)
	if !errors.Is(err, cause) {
		t.Fatalf("semantic wrapper lost errors.Is cause: %v", err)
	}
}
