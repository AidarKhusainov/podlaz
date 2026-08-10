package daemon

import (
	"strings"
	"testing"
)

func TestTunRouteLookupCommandIsReadOnly(t *testing.T) {
	name, args := tunRouteLookupCommand("198.51.100.20")
	if name != "ip" {
		t.Fatalf("command=%q, want ip", name)
	}
	joined := strings.Join(args, " ")
	if joined != "-4 route get 198.51.100.20" {
		t.Fatalf("route lookup args=%q, want a single read-only route get", joined)
	}
	if strings.Contains(joined, "flush") {
		t.Fatalf("route lookup must not mutate route cache: %q", joined)
	}
}
