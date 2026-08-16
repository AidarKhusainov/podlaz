package e2e_test

import (
	"errors"
	"os/exec"
	"testing"
)

func TestFinishExitTrapPromotesCleanupFailureAfterSuccessfulBody(t *testing.T) {
	result := runBash(t, t.TempDir(), `
set -Eeuo pipefail
source ./lib/e2e.sh
source ./lib/exit_trap.sh
cleanup() {
  local saved=$?
  finish_exit_trap "${saved}" 1
}
trap cleanup EXIT
true
`)
	if result.err == nil {
		t.Fatal("cleanup failure after a successful body must fail the shell")
	}
	var exitErr *exec.ExitError
	if !errors.As(result.err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("cleanup failure should exit 1, got %v", result.err)
	}
}

func TestFinishExitTrapPreservesOriginalBodyFailure(t *testing.T) {
	result := runBash(t, t.TempDir(), `
set -Eeuo pipefail
source ./lib/e2e.sh
source ./lib/exit_trap.sh
cleanup() {
  local saved=$?
  finish_exit_trap "${saved}" 1
}
trap cleanup EXIT
exit 7
`)
	var exitErr *exec.ExitError
	if !errors.As(result.err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("cleanup must preserve an existing body failure, got %v", result.err)
	}
}
