package executor

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

const foreignDefaultDNSStatus = `Global
       Protocols: +LLMNR

Link 9 (wg0)
    Current Scopes: none
         Protocols: +DefaultRoute
Current DNS Server: 198.51.100.53
       DNS Servers: 198.51.100.53
        DNS Domain: ~.`

const nonConflictingResolvedStatus = `Global
       Protocols: +LLMNR

Link 2 (wlan0)
    Current Scopes: DNS
         Protocols: +DefaultRoute
       DNS Servers: 192.0.2.53
        DNS Domain: ~corp.example.test`

func TestDefaultDNSOwnershipSnapshotOnlyBlocksProvenCompetingDefaultDomain(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*netsnapshot.DNS)
		foreign bool
		unknown bool
	}{
		{name: "ordinary desktop"},
		{name: "split DNS", mutate: func(d *netsnapshot.DNS) {
			d.ResolvedLinks = append(d.ResolvedLinks, netsnapshot.ResolvedLink{Name: "wg0", DNSServers: []string{"198.51.100.53"}, DNSDomains: []string{"~corp.example.test"}})
		}},
		{name: "foreign tunnel with no DNS", mutate: func(d *netsnapshot.DNS) {
			d.ResolvedLinks = append(d.ResolvedLinks, netsnapshot.ResolvedLink{Name: "wg0"})
		}},
		{name: "stale domain without ownership evidence", mutate: func(d *netsnapshot.DNS) {
			d.ResolvedLinks = append(d.ResolvedLinks, netsnapshot.ResolvedLink{Name: "wg0", DNSDomains: []string{"~."}})
		}},
		{name: "configured foreign default DNS with inactive scope", mutate: func(d *netsnapshot.DNS) {
			d.ResolvedLinks = append(d.ResolvedLinks, netsnapshot.ResolvedLink{Name: "wg0", CurrentScopes: []string{"none"}, DNSServers: []string{"198.51.100.53"}, DNSDomains: []string{"~."}})
		}, foreign: true},
		{name: "only +DefaultRoute and default domain", mutate: func(d *netsnapshot.DNS) {
			d.ResolvedLinks = append(d.ResolvedLinks, netsnapshot.ResolvedLink{Name: "wg0", Protocols: []string{"+DefaultRoute"}, DNSDomains: []string{"~."}})
		}, foreign: true},
		{name: "resolver unavailable", mutate: func(d *netsnapshot.DNS) {
			d.Resolved.Status = netsnapshot.StatusUnknown
		}, unknown: true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			dns := netsnapshot.FakeResolvedDesktop().DNS
			if tt.mutate != nil {
				tt.mutate(&dns)
			}
			err := CheckDefaultDNSOwnershipSnapshot(dns, "podlaz0")
			if !tt.foreign && !tt.unknown {
				if err != nil {
					t.Fatalf("non-competing DNS must be accepted: %v", err)
				}
				return
			}
			var ownership *DefaultDNSOwnershipError
			if !errors.As(err, &ownership) || ownership.Unknown != tt.unknown {
				t.Fatalf("ownership = %#v, want foreign=%t unknown=%t", err, tt.foreign, tt.unknown)
			}
			if strings.Contains(err.Error(), "198.51.100.53") {
				t.Fatalf("foreign DNS server leaked: %v", err)
			}
		})
	}
}

func TestDefaultDNSOwnershipLiveEvidenceIsReadOnlyAndFailClosed(t *testing.T) {
	cases := []struct {
		name    string
		output  string
		fail    bool
		unknown bool
	}{
		{name: "foreign owner", output: foreignDefaultDNSStatus, fail: true},
		{name: "split DNS", output: nonConflictingResolvedStatus},
		{name: "global only", output: "Global\n  Protocols: +LLMNR"},
		{name: "empty output", output: "", fail: true, unknown: true},
		{name: "partial malformed link header", output: "Global\nLink 9 (wg0", fail: true, unknown: true},
		{name: "malformed trailing link", output: nonConflictingResolvedStatus + "\nLink broken", fail: true, unknown: true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			runner := &recordingRunner{stdout: tt.output}
			err := (ResolvedDNSExecutor{Runner: runner}).CheckDefaultDNSOwnership(context.Background(), "podlaz0")
			if (err != nil) != tt.fail {
				t.Fatalf("ownership check error = %v, want failure=%t", err, tt.fail)
			}
			if tt.fail {
				var ownership *DefaultDNSOwnershipError
				if !errors.As(err, &ownership) || ownership.Unknown != tt.unknown {
					t.Fatalf("wrong ownership classification: %T %v", err, err)
				}
			}
			if !reflect.DeepEqual(runner.commands, [][]string{{"resolvectl", "status", "--no-pager"}}) {
				t.Fatalf("DNS ownership check must only read: %#v", runner.commands)
			}
		})
	}
	runner := &recordingRunner{err: errors.New("private DNS server 198.51.100.53")}
	err := (ResolvedDNSExecutor{Runner: runner}).CheckDefaultDNSOwnership(context.Background(), "podlaz0")
	var unknown *DefaultDNSOwnershipError
	if !errors.As(err, &unknown) || !unknown.Unknown || strings.Contains(err.Error(), "198.51.100.53") {
		t.Fatalf("failed resolver observation must be typed, unknown, and redacted: %v", err)
	}
}

func TestDNSAwareTunExecutorRechecksDNSOwnerBeforeAnyNetworkMutation(t *testing.T) {
	recorder := &callRecorder{}
	resolved := &recordingRunner{stdout: foreignDefaultDNSStatus}
	exec := DNSAwareTunExecutor{
		Base: TunExecutor{TunDevice: fakeTun{rec: recorder}, Routes: fakeRoutes{rec: recorder}, PolicyRules: fakeRules{rec: recorder}},
		DNS:  ResolvedDNSExecutor{Runner: resolved},
	}
	plan := executorPlanForTest()
	plan.DNS = dnsPlanForTest()

	steps, err := exec.ApplyWithStepSink(context.Background(), plan, nil)
	var ownership *DefaultDNSOwnershipError
	if !errors.As(err, &ownership) || ownership.Unknown {
		t.Fatalf("expected a typed pre-mutation default DNS conflict: %v", err)
	}
	if len(steps) != 0 || len(recorder.calls) != 0 {
		t.Fatalf("pre-mutation default DNS conflict changed networking: steps=%#v, calls=%#v", steps, recorder.calls)
	}
	if got := countResolvedStatusCommands(resolved.commands); got != 1 {
		t.Fatalf("unexpected DNS status call count: %d", got)
	}
}

func TestDNSAwareTunExecutorRefusesForeignOwnerAppearingDuringBaseApply(t *testing.T) {
	recorder := &callRecorder{}
	resolved := &recordingRunner{stdout: foreignDefaultDNSStatus, results: []CommandResult{
		{Stdout: nonConflictingResolvedStatus},
		{Stdout: foreignDefaultDNSStatus},
	}}
	exec := DNSAwareTunExecutor{
		Base: TunExecutor{TunDevice: fakeTun{rec: recorder}, Routes: fakeRoutes{rec: recorder}, PolicyRules: fakeRules{rec: recorder}},
		DNS:  ResolvedDNSExecutor{Runner: resolved},
	}
	plan := executorPlanForTest()
	plan.DNS = dnsPlanForTest()

	steps, err := exec.ApplyWithStepSink(context.Background(), plan, nil)
	var ownership *DefaultDNSOwnershipError
	if !errors.As(err, &ownership) || ownership.Unknown {
		t.Fatalf("expected competing owner discovered after base apply: %v", err)
	}
	if len(steps) != 4 || len(recorder.calls) == 0 {
		t.Fatalf("already-applied Podlaz routing must retain exact rollback steps: %#v, calls=%#v", steps, recorder.calls)
	}
	for _, command := range resolved.commands {
		if len(command) > 1 && command[1] != "status" {
			t.Fatalf("foreign owner appeared before DNS mutation, but executor wrote DNS: %#v", resolved.commands)
		}
	}
}

func TestDNSOwnerAppearingAtVerificationIsTypedAndNotRetried(t *testing.T) {
	plan := dnsPlanForTest()
	resolved := &recordingRunner{stdout: foreignDefaultDNSStatus}
	err := (ResolvedDNSExecutor{Runner: resolved, VerifyAttempts: 3, Sleep: noResolvedDNSTestSleep}).Verify(context.Background(), plan)
	var ownership *DefaultDNSOwnershipError
	if !errors.As(err, &ownership) || ownership.Unknown {
		t.Fatalf("foreign owner during verify must fail with typed conflict: %v", err)
	}
	if countResolvedStatusCommands(resolved.commands) != 1 {
		t.Fatalf("conclusive foreign owner must not be retried: %#v", resolved.commands)
	}
}

func TestDefaultDNSOwnershipSafeLinkName(t *testing.T) {
	err := (&DefaultDNSOwnershipError{Link: "foreign\n198.51.100.53"}).Error()
	if strings.Contains(err, "198.51.100.53") || strings.Contains(err, "\n") {
		t.Fatalf("unsanitized foreign link escaped into error: %q", err)
	}
}

// Keep package-level compiler coverage of the DNS plan types composed above.
var _ = planner.DNSActionConfigure
var _ = http.StatusConflict
