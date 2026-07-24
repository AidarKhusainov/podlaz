package executor

import (
	"context"
	"reflect"
	"testing"
)

func TestResolvedDNSExecutorApplyRefreshesStaleLinkBeforeConfiguration(t *testing.T) {
	runner := &recordingRunner{
		results: []CommandResult{{
			ExitCode: 1,
			Stderr:   `Failed to resolve interface "podlaz0": No such device` + "\n",
		}},
		errs: []error{executorTestExitError{code: 1}},
	}

	step, err := (ResolvedDNSExecutor{Runner: runner}).Apply(context.Background(), dnsPlanForTest())
	if err != nil {
		t.Fatalf("refresh stale resolved link before apply: %v", err)
	}
	if step.Kind != "dns" || step.Target != "podlaz0" || step.Owner != OwnerDNS {
		t.Fatalf("unexpected DNS step: %#v", step)
	}

	want := [][]string{
		{"resolvectl", "revert", "podlaz0"},
		{"resolvectl", "dns", "podlaz0", "1.1.1.1"},
		{"resolvectl", "domain", "podlaz0", "~."},
		{"resolvectl", "default-route", "podlaz0", "yes"},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("unexpected refresh/apply commands:\nwant %#v\n got %#v", want, runner.commands)
	}
}
