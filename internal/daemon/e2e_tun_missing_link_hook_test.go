package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestE2EDNSMissingLinkRollbackHookPausesAfterApplyUntilReleased(t *testing.T) {
	hookDir := t.TempDir()
	t.Setenv(e2eTunHookGateEnv, "true")
	t.Setenv(e2eTunHookPhaseEnv, e2eTunHookDNSMissingLinkRollbackPhase)
	t.Setenv(e2eTunHookDirEnv, hookDir)
	t.Setenv(e2eTunHookTimeoutSecondsEnv, "2")

	executor := e2eHookDNSMissingLinkRollbackExecutor{delegate: recordingDNSExecutor{}}
	type result struct {
		step netexecutor.Step
		err  error
	}
	resultCh := make(chan result, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		step, err := executor.Apply(ctx, planner.TunDNSPlan{
			TargetLink: "podlaz0",
			Action:     planner.DNSActionConfigure,
			Servers:    []string{"192.0.2.53"},
		})
		resultCh <- result{step: step, err: err}
	}()

	ready := filepath.Join(hookDir, e2eTunHookDNSMissingLinkReadyFile)
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("missing-link hook did not publish ready marker: %s", ready)
		}
		time.Sleep(10 * time.Millisecond)
	}

	select {
	case got := <-resultCh:
		t.Fatalf("hook returned before release marker: %#v", got)
	default:
	}

	release := filepath.Join(hookDir, e2eTunHookDNSMissingLinkContinueFile)
	if err := os.WriteFile(release, []byte("continue\n"), 0o600); err != nil {
		t.Fatalf("release missing-link hook: %v", err)
	}

	select {
	case got := <-resultCh:
		if got.step.Kind != "dns" || got.step.Owner != netexecutor.OwnerDNS {
			t.Fatalf("hook must preserve applied DNS step: %#v", got.step)
		}
		if got.err != nil {
			t.Fatalf("hook must let production verification observe the removed link: %v", got.err)
		}
	case <-ctx.Done():
		t.Fatalf("missing-link hook did not resume: %v", ctx.Err())
	}

	events, err := os.ReadFile(filepath.Join(hookDir, e2eTunHookEventsFile))
	if err != nil {
		t.Fatalf("read hook events: %v", err)
	}
	for _, want := range []string{"dns-missing-link-ready", "dns-missing-link-released"} {
		if !strings.Contains(string(events), want) {
			t.Fatalf("missing hook event %q: %s", want, events)
		}
	}
}
