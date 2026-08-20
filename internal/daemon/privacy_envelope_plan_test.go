package daemon

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestAllocatePrivacyEnvelopeUsesStableSessionScopedCandidate(t *testing.T) {
	observer := &privacyEnvelopeObserverStub{}
	protection, plan, err := allocatePrivacyEnvelope(
		context.Background(),
		"00112233445566778899aabbccddeeff",
		"podlaz0",
		[]string{"198.51.100.20", "192.0.2.10", "192.0.2.10"},
		observer,
	)
	if err != nil {
		t.Fatalf("allocate privacy envelope: %v", err)
	}
	if plan.Family != "inet" || plan.Table != "podlaz_pe_001122334455" {
		t.Fatalf("unexpected exact envelope target: %#v", plan)
	}
	if protection.Table != plan.Table || protection.Family != plan.Family || protection.State != networkSessionProtectionArming {
		t.Fatalf("protection authority does not bind the exact planned table: %#v", protection)
	}
	wantEndpoints := []string{"192.0.2.10", "198.51.100.20"}
	if !reflect.DeepEqual(protection.BootstrapIPv4, wantEndpoints) {
		t.Fatalf("bootstrap endpoints = %#v, want %#v", protection.BootstrapIPv4, wantEndpoints)
	}
	if protection.CompositionVersion != privacyEnvelopeCompositionVersion {
		t.Fatalf("composition version = %d, want %d", protection.CompositionVersion, privacyEnvelopeCompositionVersion)
	}
}

func TestAllocatePrivacyEnvelopeSkipsOccupiedCandidateWithoutInspectingOwnership(t *testing.T) {
	observer := &privacyEnvelopeObserverStub{occupied: map[string]bool{
		"inet/podlaz_pe_001122334455": true,
	}}
	protection, _, err := allocatePrivacyEnvelope(
		context.Background(),
		"00112233445566778899aabbccddeeff",
		"podlaz0",
		[]string{"192.0.2.10"},
		observer,
	)
	if err != nil {
		t.Fatalf("allocate around occupied envelope candidate: %v", err)
	}
	if protection.Table != "podlaz_pe_001122334455_1" {
		t.Fatalf("allocated table = %q, want collision-free suffix candidate", protection.Table)
	}
	if len(observer.seen) < 2 || observer.seen[0] != "inet/podlaz_pe_001122334455" || observer.seen[1] != "inet/podlaz_pe_001122334455_1" {
		t.Fatalf("candidate observation order = %#v", observer.seen)
	}
}

func TestAllocatePrivacyEnvelopeFailsClosedWhenBoundedCandidatesExhausted(t *testing.T) {
	observer := &privacyEnvelopeObserverStub{occupied: map[string]bool{}}
	base := "podlaz_pe_001122334455"
	observer.occupied["inet/"+base] = true
	for i := 1; i < privacyEnvelopeCandidateLimit; i++ {
		observer.occupied[fmt.Sprintf("inet/%s_%d", base, i)] = true
	}
	if _, _, err := allocatePrivacyEnvelope(
		context.Background(),
		"00112233445566778899aabbccddeeff",
		"podlaz0",
		[]string{"192.0.2.10"},
		observer,
	); err == nil {
		t.Fatal("expected bounded privacy envelope allocation exhaustion")
	}
	if len(observer.seen) != privacyEnvelopeCandidateLimit {
		t.Fatalf("observed %d candidates, want bounded %d", len(observer.seen), privacyEnvelopeCandidateLimit)
	}
}

func TestPrivacyEnvelopeCompositionAllowsOnlyProtectedAndMinimalControlPaths(t *testing.T) {
	_, plan, err := allocatePrivacyEnvelope(
		context.Background(),
		"00112233445566778899aabbccddeeff",
		"podlaz0",
		[]string{"192.0.2.10"},
		&privacyEnvelopeObserverStub{},
	)
	if err != nil {
		t.Fatalf("allocate privacy envelope: %v", err)
	}
	var expressions []string
	for _, rule := range plan.Rules {
		expressions = append(expressions, strings.TrimSpace(rule.Expr)+" -> "+rule.Verdict)
	}
	joined := strings.Join(expressions, "\n")
	for _, want := range []string{
		`oifname "lo" -> accept`,
		`oifname "podlaz0" -> accept`,
		`ip daddr 192.0.2.10 -> accept`,
		`meta nfproto ipv4 udp sport 68 udp dport 67 -> accept`,
		`meta nfproto ipv6 udp sport 546 udp dport 547 -> accept`,
		`meta nfproto ipv6 icmpv6 type { nd-router-solicit, nd-neighbor-solicit, nd-neighbor-advert } -> accept`,
		`-> reject`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("privacy envelope is missing required narrow rule %q:\n%s", want, joined)
		}
	}
	for _, forbidden := range []string{"ct state", "dport 53", `oifname !=`, "0.0.0.0/0", "::/0"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("privacy envelope contains broad direct allowance %q:\n%s", forbidden, joined)
		}
	}
	last := plan.Rules[len(plan.Rules)-1]
	if strings.TrimSpace(last.Expr) != "" || last.Verdict != "reject" {
		t.Fatalf("last rule must fail closed for every non-exempt packet: %#v", last)
	}
}

func TestPrivacyEnvelopePlanReconstructsExactlyFromDurableAuthority(t *testing.T) {
	protection, plan, err := allocatePrivacyEnvelope(
		context.Background(),
		"00112233445566778899aabbccddeeff",
		"podlaz0",
		[]string{"192.0.2.10", "198.51.100.20"},
		&privacyEnvelopeObserverStub{},
	)
	if err != nil {
		t.Fatalf("allocate privacy envelope: %v", err)
	}
	reconstructed, err := privacyEnvelopePlanFromAuthority(protection)
	if err != nil {
		t.Fatalf("reconstruct privacy envelope: %v", err)
	}
	if !reflect.DeepEqual(reconstructed, plan) {
		t.Fatalf("reconstructed envelope differs from persisted authority:\nwant %#v\n got %#v", plan, reconstructed)
	}
}

func TestPrivacyEnvelopePlanRejectsInvalidIdentityInputs(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
		iface     string
		endpoints []string
	}{
		{name: "session", sessionID: "not-a-session", iface: "podlaz0", endpoints: []string{"192.0.2.10"}},
		{name: "interface", sessionID: "00112233445566778899aabbccddeeff", iface: "bad interface", endpoints: []string{"192.0.2.10"}},
		{name: "endpoint", sessionID: "00112233445566778899aabbccddeeff", iface: "podlaz0", endpoints: []string{"2001:db8::10"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := allocatePrivacyEnvelope(context.Background(), tt.sessionID, tt.iface, tt.endpoints, &privacyEnvelopeObserverStub{}); err == nil {
				t.Fatal("expected invalid exact identity input to fail closed")
			}
		})
	}
}

type privacyEnvelopeObserverStub struct {
	occupied map[string]bool
	seen     []string
	err      error
}

func (s *privacyEnvelopeObserverStub) PrivacyEnvelopeTableExists(_ context.Context, family, table string) (bool, error) {
	s.seen = append(s.seen, family+"/"+table)
	if s.err != nil {
		return false, s.err
	}
	return s.occupied[family+"/"+table], nil
}
