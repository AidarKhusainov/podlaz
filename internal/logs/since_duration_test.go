package logs

import (
	"reflect"
	"testing"
)

func TestParseSinceDurationAcceptsDocumentedGrammar(t *testing.T) {
	tests := []string{"30s", "15m", "2h", "36h", "720h"}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			since, err := ParseSinceDuration(input)
			if err != nil {
				t.Fatalf("ParseSinceDuration(%q) failed: %v", input, err)
			}
			got := BuildJournalctlArgs(Options{Since: since})
			want := []string{"--system", "--unit", DaemonUnit, "--no-pager", "--output", "short", "--since", "-" + input}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("journalctl args mismatch\nwant: %#v\n got: %#v", want, got)
			}
		})
	}
}

func TestParseSinceDurationCanonicalizesLeadingZeros(t *testing.T) {
	since, err := ParseSinceDuration("0001h")
	if err != nil {
		t.Fatalf("ParseSinceDuration with leading zeros failed: %v", err)
	}
	if since != "1h" {
		t.Fatalf("expected canonical duration 1h, got %q", since)
	}
	got := BuildJournalctlArgs(Options{Since: "0001h"})
	want := []string{"--system", "--unit", DaemonUnit, "--no-pager", "--output", "short", "--since", "-1h"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("journalctl args mismatch\nwant: %#v\n got: %#v", want, got)
	}
}

func TestParseSinceDurationRejectsUndocumentedOrUnsafeForms(t *testing.T) {
	tests := []string{
		"", "0s", "0m", "0h", "00h", "-1h", "+1h", "1.5h", "1h30m",
		"1d", "1ms", "1us", "1µs", "1ns", "yesterday", "721h", "999999999999999999999h",
	}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			if _, err := ParseSinceDuration(input); err == nil {
				t.Fatalf("ParseSinceDuration(%q) unexpectedly succeeded", input)
			}
		})
	}
}

func TestBuildJournalctlArgsNormalizesSinceForFollowAndCore(t *testing.T) {
	since, err := ParseSinceDuration("36h")
	if err != nil {
		t.Fatal(err)
	}
	got := BuildJournalctlArgs(Options{Since: since, Follow: true, Core: true})
	want := []string{"--system", "--unit", DaemonUnit, "--no-pager", "--output", "short", "--since", "-36h", "--follow"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("journalctl args mismatch\nwant: %#v\n got: %#v", want, got)
	}
}
