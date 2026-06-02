package render

import (
	"strings"
	"testing"
)

func TestRedactMasksSensitiveOutput(t *testing.T) {
	input := strings.Join([]string{
		"subscription=https://example.com/sub?token=very-secret-token",
		"profile=123e4567-e89b-12d3-a456-426614174000",
		"password=hunter2",
		"api_key=abcdef",
	}, "\n")

	got := Redact(input)
	for _, forbidden := range []string{"very-secret-token", "hunter2", "abcdef", "123e4567-e89b-12d3-a456-426614174000"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("redacted output leaked %q in %q", forbidden, got)
		}
	}
	for _, want := range []string{"https://example.com/sub?REDACTED", "password=REDACTED", "api_key=REDACTED", "123e…4000"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected redacted output to contain %q, got %q", want, got)
		}
	}
}

func TestRedactCollapsesWhitespace(t *testing.T) {
	got := Redact("line one\n\tline two")
	if got != "line one line two" {
		t.Fatalf("expected collapsed whitespace, got %q", got)
	}
}
