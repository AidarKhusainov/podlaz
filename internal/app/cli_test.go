package app

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunCLIVersion(t *testing.T) {
	var out bytes.Buffer

	err := runCLI(context.Background(), []string{"version"}, &out)
	if err != nil {
		t.Fatalf("version failed: %v", err)
	}

	if got := out.String(); !strings.Contains(got, "tunwarden") {
		t.Fatalf("expected version output to contain binary name, got %q", got)
	}
}

func TestRunCLIUnknownCommand(t *testing.T) {
	var out bytes.Buffer

	err := runCLI(context.Background(), []string{"unknown"}, &out)
	if err == nil {
		t.Fatal("expected unknown command to fail")
	}
}
