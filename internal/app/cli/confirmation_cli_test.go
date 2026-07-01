package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseDefaultYesConfirmation(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		confirmed bool
		valid     bool
	}{
		{name: "empty", input: "\n", confirmed: true, valid: true},
		{name: "y", input: "y\n", confirmed: true, valid: true},
		{name: "yes uppercase", input: "YES\n", confirmed: true, valid: true},
		{name: "n", input: "n\n", confirmed: false, valid: true},
		{name: "no uppercase", input: "NO\n", confirmed: false, valid: true},
		{name: "invalid", input: "yep\n", confirmed: false, valid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			confirmed, valid := parseDefaultYesConfirmation(tt.input)
			if confirmed != tt.confirmed || valid != tt.valid {
				t.Fatalf("expected confirmed=%v valid=%v, got confirmed=%v valid=%v", tt.confirmed, tt.valid, confirmed, valid)
			}
		})
	}
}

func TestConfirmDefaultYesRetriesInvalidInput(t *testing.T) {
	var out bytes.Buffer
	err := confirmDefaultYes(&out, strings.NewReader("maybe\n\n"), "Continue", "test", "canceled")
	if err != nil {
		t.Fatalf("expected retry then default yes confirmation, got %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Continue [Y/n]:") || !strings.Contains(got, "Please answer y or n.") {
		t.Fatalf("expected visible default prompt and retry guidance, got %q", got)
	}
}

func TestConfirmDefaultYesCancel(t *testing.T) {
	var out bytes.Buffer
	err := confirmDefaultYes(&out, strings.NewReader("no\n"), "Continue", "test", "canceled")
	if err == nil {
		t.Fatal("expected no to cancel")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("expected cancel exit code 1, got %d", got)
	}
	if !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected cancel error, got %v", err)
	}
}
