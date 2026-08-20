package snapshot

import (
	"context"
	"testing"
)

func TestParseIPv4PolicyRulesRetainsHistoricalLookingAndForeignRules(t *testing.T) {
	rules, err := ParseIPv4PolicyRules(`0: from all lookup local
9999: from all to 203.0.113.10 lookup main
10000: from all lookup 51820
12000: from 192.0.2.0/24 lookup 60000
32766: from all lookup main
32767: from all lookup default`)
	if err != nil {
		t.Fatalf("ParseIPv4PolicyRules() error = %v", err)
	}
	if len(rules) != 3 {
		t.Fatalf("expected three non-system rules, got %#v", rules)
	}
	assertPolicyRule(t, rules[0], "9999", "to 203.0.113.10", "main")
	assertPolicyRule(t, rules[1], "10000", "from all", "51820")
	assertPolicyRule(t, rules[2], "12000", "from 192.0.2.0/24", "60000")
}

func TestParseIPv4PolicyRulesRejectsMalformedPriority(t *testing.T) {
	if _, err := ParseIPv4PolicyRules("not-a-priority: from all lookup 60000"); err == nil {
		t.Fatal("expected malformed rule priority to fail closed")
	}
}

func TestIPv4PolicyRulesMarksInventoryUnknownOnMalformedOutput(t *testing.T) {
	runner := fakeRunner{
		commands: map[string]CommandResult{
			"/usr/sbin/ip -4 rule show": {Stdout: "broken: from all lookup 60000"},
		},
	}

	inventory := ipv4PolicyRules(context.Background(), runner, "/usr/sbin/ip")
	if inventory.Inspection.Status != StatusUnknown {
		t.Fatalf("expected malformed policy-rule inventory to be unknown, got %#v", inventory)
	}
	if len(inventory.Rules) != 0 {
		t.Fatalf("malformed inventory must not expose partial authoritative rules: %#v", inventory.Rules)
	}
}

func assertPolicyRule(t *testing.T, got PolicyRoutingSignal, priority, selector, table string) {
	t.Helper()
	if got.Kind != "rule" || got.Priority != priority || got.Selector != selector || got.Table != table {
		t.Fatalf("unexpected policy rule: got %#v, want priority=%s selector=%q table=%q", got, priority, selector, table)
	}
}
