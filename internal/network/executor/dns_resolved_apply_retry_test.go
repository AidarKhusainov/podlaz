package executor

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestResolvedDNSExecutorApplyRetriesTransientMissingLinkRegistration(t *testing.T) {
	runner := &recordingRunner{
		results: []CommandResult{
			{ExitCode: 1, Stderr: `Failed to resolve interface "podlaz0": No such device` + "\n"},
			{ExitCode: 1, Stderr: `Failed to resolve interface "podlaz0": No such device` + "\n"},
			{},
			{},
			{},
		},
		errs: []error{
			executorTestExitError{code: 1},
			executorTestExitError{code: 1},
		},
	}
	executor := ResolvedDNSExecutor{
		Runner:            runner,
		ApplyAttempts:     2,
		ApplyPollInterval: time.Nanosecond,
		Sleep:             noResolvedDNSTestSleep,
	}

	if _, err := executor.Apply(context.Background(), dnsPlanForTest()); err != nil {
		t.Fatalf("apply DNS after delayed resolved link registration: %v", err)
	}
	want := [][]string{
		{"resolvectl", "revert", "podlaz0"},
		{"resolvectl", "dns", "podlaz0", "1.1.1.1"},
		{"resolvectl", "dns", "podlaz0", "1.1.1.1"},
		{"resolvectl", "domain", "podlaz0", "~."},
		{"resolvectl", "default-route", "podlaz0", "yes"},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("unexpected resolved retry commands:\nwant %#v\n got %#v", want, runner.commands)
	}
}

func TestResolvedDNSExecutorApplyDoesNotRetryUnexpectedFailure(t *testing.T) {
	runner := &recordingRunner{
		results: []CommandResult{
			{},
			{ExitCode: 1, Stderr: "Access denied"},
		},
		errs: []error{nil, errors.New("exit status 1")},
	}
	executor := ResolvedDNSExecutor{
		Runner:            runner,
		ApplyAttempts:     5,
		ApplyPollInterval: time.Nanosecond,
		Sleep:             noResolvedDNSTestSleep,
	}

	if _, err := executor.Apply(context.Background(), dnsPlanForTest()); err == nil {
		t.Fatal("unexpected resolvectl failure must remain blocking")
	}
	if len(runner.commands) != 2 {
		t.Fatalf("unexpected failure must not be retried, commands=%#v", runner.commands)
	}
}
