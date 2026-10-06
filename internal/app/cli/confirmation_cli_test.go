package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseDefaultNoConfirmation(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		confirmed bool
		valid     bool
	}{
		{name: "empty", input: "\n", confirmed: false, valid: true},
		{name: "y", input: "y\n", confirmed: true, valid: true},
		{name: "yes uppercase", input: "YES\n", confirmed: true, valid: true},
		{name: "n", input: "n\n", confirmed: false, valid: true},
		{name: "no uppercase", input: "NO\n", confirmed: false, valid: true},
		{name: "invalid", input: "yep\n", confirmed: false, valid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			confirmed, valid := parseDefaultNoConfirmation(tt.input)
			if confirmed != tt.confirmed || valid != tt.valid {
				t.Fatalf("expected confirmed=%v valid=%v, got confirmed=%v valid=%v", tt.confirmed, tt.valid, confirmed, valid)
			}
		})
	}
}

func TestConfirmDefaultNoRetriesThenRequiresExplicitYes(t *testing.T) {
	var out bytes.Buffer
	err := confirmDefaultNo(&out, strings.NewReader("maybe\nyes\n"), "Continue", "test", "canceled")
	if err != nil {
		t.Fatalf("explicit yes after retry failed: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Continue [y/N]:") || !strings.Contains(got, "Please answer y or n.") {
		t.Fatalf("expected default-no prompt and retry guidance, got %q", got)
	}
}

func TestConfirmDefaultNoEmptyLineCancels(t *testing.T) {
	var out bytes.Buffer
	err := confirmDefaultNo(&out, strings.NewReader("\n"), "Continue", "test", "canceled")
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("empty input err=%v exit=%d, want cancellation", err, ExitCode(err))
	}
}

func TestConfirmDefaultNoEmptyEOFCancels(t *testing.T) {
	var out bytes.Buffer
	err := confirmDefaultNo(&out, strings.NewReader(""), "Continue", "test", "canceled")
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("empty EOF err=%v exit=%d, want cancellation", err, ExitCode(err))
	}
}
