package daemon

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestPrivacyEnvelopeAllocationSkipsOccupiedGeneratedTablesDeterministically(t *testing.T) {
	observer := &privacyEnvelopeObserverStub{occupied: map[string]bool{
		"inet/podlaz_pe_001122334455":   true,
		"inet/podlaz_pe_001122334455_1": true,
	}}
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
	if protection.Table != "podlaz_pe_001122334455_2" || plan.Table != protection.Table {
		t.Fatalf("unexpected allocated table: protection=%#v plan=%#v", protection, plan)
	}
	if got, want := protection.BootstrapIPv4, []string{"192.0.2.10", "198.51.100.20"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("bootstrap endpoints = %#v, want %#v", got, want)
	}
	if got, want := observer.seen, []string{
		"inet/podlaz_pe_001122334455",
		"inet/podlaz_pe_001122334455_1",
		"inet/podlaz_pe_001122334455_2",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("candidate order = %#v, want %#v", got, want)
	}
}

func TestPrivacyEnvelopeAllocationFailsClosedWhenCandidateInspectionFails(t *testing.T) {
	observer := &privacyEnvelopeObserverStub{errAt: "inet/podlaz_pe_001122334455_1"}
	observer.occupied = map[string]bool{"inet/podlaz_pe_001122334455": true}
	if _, _, err := allocatePrivacyEnvelope(
		context.Background(),
		"00112233445566778899aabbccddeeff",
		"podlaz0",
		[]string{"192.0.2.10"},
		observer,
	); err == nil {
		t.Fatal("expected privacy envelope allocation to fail closed on inspection error")
	}
	if got, want := observer.seen, []string{"inet/podlaz_pe_001122334455", "inet/podlaz_pe_001122334455_1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected observation sequence: got %#v want %#v", got, want)
	}
}

func TestPrivacyEnvelopeAllocationIsBounded(t *testing.T) {
	observer := &privacyEnvelopeObserverStub{occupied: make(map[string]bool)}
	for i := 0; i < privacyEnvelopeCandidateLimit; i++ {
		table := "podlaz_pe_001122334455"
		if i != 0 {
			table = fmt.Sprintf("%s_%d", table, i)
		}
		observer.occupied["inet/"+table] = true
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
		`icmpv6 type { nd-router-solicit, nd-neighbor-solicit, nd-neighbor-advert } -> accept`,
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
		t.Fatalf("reconstructed plan differs:\nwant %#v\n got %#v", plan, reconstructed)
	}
}

func TestPrivacyEnvelopePlanRejectsUnsupportedCompositionVersion(t *testing.T) {
	protection := testArmedPrivacyProtection()
	protection.CompositionVersion++
	if _, err := privacyEnvelopePlanFromAuthority(protection); err == nil {
		t.Fatal("expected unsupported composition version rejection")
	}
}

type privacyEnvelopeObserverStub struct {
	occupied map[string]bool
	errAt    string
	seen     []string
}

func (o *privacyEnvelopeObserverStub) PrivacyEnvelopeTableExists(_ context.Context, family, table string) (bool, error) {
	key := family + "/" + table
	o.seen = append(o.seen, key)
	if key == o.errAt {
		return false, fmt.Errorf("synthetic observation failure")
	}
	return o.occupied[key], nil
}
