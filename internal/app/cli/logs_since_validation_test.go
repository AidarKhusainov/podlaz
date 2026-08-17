package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/logs"
)

func TestRunCLILogsRejectsInvalidSinceBeforeJournalctl(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"logs", "--since", "1h30m"}, &out, options{})
	if err == nil {
		t.Fatal("expected invalid --since to fail")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("expected usage exit code 2, got %d: %v", got, err)
	}
	if !strings.Contains(err.Error(), "invalid logs --since duration") {
		t.Fatalf("expected product-level duration error, got %v", err)
	}
	if strings.Contains(err.Error(), "journalctl is not available") {
		t.Fatalf("invalid input reached journalctl dependency lookup: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("invalid input wrote output: %q", out.String())
	}
}

func TestRunCLILogsKeepsValidSinceBackendFailureAsRuntimeError(t *testing.T) {
	backendErr := errors.New("journalctl backend failed")
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"logs", "--since", "36h"}, &out, options{
		logs: func(context.Context, io.Writer, logs.Options) error {
			return backendErr
		},
	})
	if !errors.Is(err, backendErr) {
		t.Fatalf("expected backend error %v, got %v", backendErr, err)
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("expected runtime exit code 1, got %d: %v", got, err)
	}
	if out.Len() != 0 {
		t.Fatalf("backend failure wrote output: %q", out.String())
	}
}
