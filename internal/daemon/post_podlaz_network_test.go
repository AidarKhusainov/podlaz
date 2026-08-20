package daemon

import (
	"context"
	"errors"
	"testing"

	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func TestPostPodlazNetworkVerifierAcceptsOrdinaryAndForeignRoutedBaselines(t *testing.T) {
	for _, iface := range []string{"eth0", "tun9"} {
		t.Run(iface, func(t *testing.T) {
			calls := []string{}
			verifier := postPodlazNetworkVerifier{
				collect: func(context.Context) netsnapshot.Snapshot {
					calls = append(calls, "snapshot")
					return postPodlazSnapshotForTest(netsnapshot.StatusDetected, iface)
				},
				verifyRoute: func(context.Context, string) error {
					calls = append(calls, "route")
					return nil
				},
				verifyTCP: func(context.Context, string, uint16) error {
					calls = append(calls, "tcp")
					return nil
				},
				verifyDNS: func(context.Context, string) ([]string, error) {
					calls = append(calls, "dns")
					return []string{"192.0.2.80"}, nil
				},
			}
			if err := verifier.Verify(context.Background()); err != nil {
				t.Fatalf("verify remaining network on %s: %v", iface, err)
			}
			if got := len(calls); got != 4 {
				t.Fatalf("unexpected read-only proof calls: %#v", calls)
			}
		})
	}
}

func TestPostPodlazNetworkVerifierRejectsMissingIncompleteAndPodlazDefaultPath(t *testing.T) {
	tests := []struct {
		name   string
		status netsnapshot.FindingStatus
		iface  string
	}{
		{name: "missing", status: netsnapshot.StatusMissing},
		{name: "unknown", status: netsnapshot.StatusUnknown},
		{name: "podlaz", status: netsnapshot.StatusDetected, iface: "podlaz0"},
		{name: "empty interface", status: netsnapshot.StatusDetected},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			functionalCalls := 0
			verifier := postPodlazNetworkVerifier{
				collect: func(context.Context) netsnapshot.Snapshot {
					return postPodlazSnapshotForTest(tt.status, tt.iface)
				},
				verifyRoute: func(context.Context, string) error { functionalCalls++; return nil },
				verifyTCP:   func(context.Context, string, uint16) error { functionalCalls++; return nil },
				verifyDNS: func(context.Context, string) ([]string, error) {
					functionalCalls++
					return []string{"192.0.2.80"}, nil
				},
			}
			if err := verifier.Verify(context.Background()); err == nil {
				t.Fatal("expected incomplete/non-remaining default path to fail closed")
			}
			if functionalCalls != 0 {
				t.Fatalf("functional probes ran before authoritative route evidence: %d", functionalCalls)
			}
		})
	}
}

func TestPostPodlazNetworkVerifierPropagatesFunctionalProofFailures(t *testing.T) {
	tests := []struct {
		name     string
		routeErr error
		tcpErr   error
		dnsErr   error
	}{
		{name: "route", routeErr: errors.New("synthetic route failure")},
		{name: "tcp", tcpErr: errors.New("synthetic tcp failure")},
		{name: "dns", dnsErr: errors.New("synthetic dns failure")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verifier := postPodlazNetworkVerifier{
				collect: func(context.Context) netsnapshot.Snapshot {
					return postPodlazSnapshotForTest(netsnapshot.StatusDetected, "tun9")
				},
				verifyRoute: func(context.Context, string) error { return tt.routeErr },
				verifyTCP:   func(context.Context, string, uint16) error { return tt.tcpErr },
				verifyDNS: func(context.Context, string) ([]string, error) {
					if tt.dnsErr != nil {
						return nil, tt.dnsErr
					}
					return []string{"192.0.2.80"}, nil
				},
			}
			if err := verifier.Verify(context.Background()); err == nil {
				t.Fatal("expected functional post-Podlaz proof failure")
			}
		})
	}
}

func TestProductionPostPodlazNetworkVerifierHasReadOnlyComposition(t *testing.T) {
	verifier := newPostPodlazNetworkVerifier()
	if verifier.collect == nil || verifier.verifyRoute == nil || verifier.verifyTCP == nil || verifier.verifyDNS == nil {
		t.Fatalf("production post-Podlaz verifier is incomplete: %#v", verifier)
	}
}

func postPodlazSnapshotForTest(status netsnapshot.FindingStatus, iface string) netsnapshot.Snapshot {
	return netsnapshot.Snapshot{
		OS: "linux",
		DefaultIPv4: netsnapshot.Route{
			Status:      status,
			Family:      "ipv4",
			Destination: "default",
			Interface:   iface,
			Gateway:     "192.0.2.1",
		},
	}
}
