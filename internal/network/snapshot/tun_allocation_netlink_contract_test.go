package snapshot

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

func TestTunAllocationEvidenceUsesTypedKernelIdentities(t *testing.T) {
	evidence := TunAllocationEvidence{
		IPv4Addresses: []netip.Prefix{
			netip.MustParsePrefix("192.0.2.10/24"),
			netip.MustParsePrefix("198.18.0.1/32"),
		},
		IPv4Routes: []TunAllocationRoute{
			{Destination: netip.MustParsePrefix("127.0.0.0/8"), Table: 255},
			{Destination: netip.MustParsePrefix("172.17.0.0/16"), Table: 254},
			{Default: true, Table: 254},
		},
		IPv4PolicyRules: []TunAllocationRule{
			{Priority: 100, Table: 60000},
		},
	}

	if got := evidence.IPv4Routes[0].Table; got != 255 {
		t.Fatalf("local-table route identity = %d, want 255", got)
	}
	if got := evidence.IPv4Routes[1].Destination; got != netip.MustParsePrefix("172.17.0.0/16") {
		t.Fatalf("Docker-like route destination = %s", got)
	}
	if got := evidence.IPv4PolicyRules[0].Priority; got != 100 {
		t.Fatalf("policy-rule priority = %d, want 100", got)
	}
}

func TestRetryTunAllocationEvidenceDumpDiscardsInterruptedPartialEvidence(t *testing.T) {
	attempts := 0
	partial := TunAllocationEvidence{IPv4Addresses: []netip.Prefix{netip.MustParsePrefix("198.51.100.1/32")}}
	complete := TunAllocationEvidence{IPv4Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.10/24")}}

	got, err := retryTunAllocationEvidenceDump(context.Background(), func(context.Context) (TunAllocationEvidence, error) {
		attempts++
		if attempts < tunAllocationDumpAttempts {
			return partial, errTunAllocationDumpInterrupted
		}
		return complete, nil
	})
	if err != nil {
		t.Fatalf("retryTunAllocationEvidenceDump() error = %v", err)
	}
	if attempts != tunAllocationDumpAttempts {
		t.Fatalf("attempts = %d, want %d", attempts, tunAllocationDumpAttempts)
	}
	if len(got.IPv4Addresses) != 1 || got.IPv4Addresses[0] != complete.IPv4Addresses[0] {
		t.Fatalf("interrupted partial evidence leaked into result: %#v", got)
	}
}

func TestRetryTunAllocationEvidenceDumpFailsClosedAfterInterruptedDumpExhaustion(t *testing.T) {
	attempts := 0
	partial := TunAllocationEvidence{IPv4Routes: []TunAllocationRoute{{Default: true, Table: 254}}}

	got, err := retryTunAllocationEvidenceDump(context.Background(), func(context.Context) (TunAllocationEvidence, error) {
		attempts++
		return partial, errTunAllocationDumpInterrupted
	})
	if !errors.Is(err, errTunAllocationDumpInterrupted) {
		t.Fatalf("error = %v, want interrupted dump", err)
	}
	if attempts != tunAllocationDumpAttempts {
		t.Fatalf("attempts = %d, want %d", attempts, tunAllocationDumpAttempts)
	}
	if len(got.IPv4Routes) != 0 {
		t.Fatalf("partial evidence must be discarded on exhaustion: %#v", got)
	}
}
