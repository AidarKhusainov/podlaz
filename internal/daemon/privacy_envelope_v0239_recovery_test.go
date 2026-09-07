package daemon

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
)

const v0239PrivacyEnvelopeICMPv6Expr = "meta nfproto ipv6 icmpv6 type { nd-router-solicit, nd-neighbor-solicit, nd-neighbor-advert }"

func TestV0239PrivacyEnvelopeCompositionV1ConvergesThroughTerminalRecovery(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionStateStore(runtimeDir, fixedBootID("boot-a"))
	if _, err := store.BeginOrResume(testContinuationRequest()); err != nil {
		t.Fatalf("begin Network Session: %v", err)
	}
	protection := testArmedPrivacyProtection()
	if err := store.SetProtection(&protection); err != nil {
		t.Fatalf("persist v0.2.39 composition-v1 authority: %v", err)
	}
	if err := store.SetIntent(networkSessionIntentTerminal); err != nil {
		t.Fatalf("persist terminal intent: %v", err)
	}

	// v0.2.39 composition v1 emitted an explicit nfproto ipv6 dependency before
	// the ICMPv6 payload match. nft canonicalizes that dependency away because
	// the ICMPv6 payload itself implies IPv6. Keep this historical spelling as
	// an explicit compatibility boundary rather than widening generic equality.
	historicalPlan, err := privacyEnvelopePlanFromAuthority(protection)
	if err != nil {
		t.Fatalf("reconstruct composition-v1 plan: %v", err)
	}
	historicalPlan.Rules[5].Expr = v0239PrivacyEnvelopeICMPv6Expr
	if err := (netexecutor.PrivacyEnvelopeExecutor{Runner: v0239PrivacyEnvelopeRunner{}}).Verify(context.Background(), historicalPlan); err != nil {
		t.Fatalf("v0.2.39 composition-v1 semantics must match canonical live nftables JSON: %v", err)
	}

	executor := &v0239TerminalRecoveryExecutor{present: true, store: store}
	if err := continuePersistedNetworkSessionTeardownWith(
		context.Background(),
		store,
		executor,
		func(context.Context) error { return nil },
	); err != nil {
		t.Fatalf("terminal recovery of v0.2.39 Privacy Envelope: %v", err)
	}
	if executor.removeCalls != 1 {
		t.Fatalf("exact removal calls=%d, want 1", executor.removeCalls)
	}
	if executor.removedFamily != protection.Family || executor.removedTable != protection.Table {
		t.Fatalf("terminal recovery removed %q %q, want exact %q %q", executor.removedFamily, executor.removedTable, protection.Family, protection.Table)
	}
	if !executor.sawCompositionV1Removing {
		t.Fatal("composition-v1 authority must remain durable through removing state; recovery must not require schema migration")
	}
	if _, exists, err := store.Load(); err != nil || exists {
		t.Fatalf("converged terminal recovery must clear Network Session authority, exists=%v err=%v", exists, err)
	}
}

func TestV0239PrivacyEnvelopeCompatibilityDoesNotAuthorizeGenuineDrift(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionStateStore(runtimeDir, fixedBootID("boot-a"))
	if _, err := store.BeginOrResume(testContinuationRequest()); err != nil {
		t.Fatalf("begin Network Session: %v", err)
	}
	protection := testArmedPrivacyProtection()
	if err := store.SetProtection(&protection); err != nil {
		t.Fatalf("persist composition-v1 authority: %v", err)
	}
	if err := store.SetIntent(networkSessionIntentTerminal); err != nil {
		t.Fatalf("persist terminal intent: %v", err)
	}

	executor := &v0239TerminalRecoveryExecutor{present: true, drift: true, store: store}
	err := continuePersistedNetworkSessionTeardownWith(
		context.Background(),
		store,
		executor,
		func(context.Context) error { return nil },
	)
	if err == nil {
		t.Fatal("genuine live Privacy Envelope drift must fail closed")
	}
	if executor.removeCalls != 0 {
		t.Fatalf("drifted live table must not be removed, calls=%d", executor.removeCalls)
	}
	state, exists, loadErr := store.Load()
	if loadErr != nil || !exists || state.Protection == nil {
		t.Fatalf("failed drift recovery lost durable protection authority: exists=%v state=%#v err=%v", exists, state, loadErr)
	}
	if state.Protection.State != networkSessionProtectionArmed || state.Protection.CompositionVersion != privacyEnvelopeCompositionVersion {
		t.Fatalf("failed drift recovery changed authority unexpectedly: %#v", state.Protection)
	}
}

type v0239TerminalRecoveryExecutor struct {
	present                  bool
	drift                    bool
	store                    networkSessionStateStore
	removeCalls              int
	removedFamily            string
	removedTable             string
	sawCompositionV1Removing bool
}

func (e *v0239TerminalRecoveryExecutor) PrivacyEnvelopeTableExists(context.Context, string, string) (bool, error) {
	return e.present, nil
}

func (e *v0239TerminalRecoveryExecutor) Exists(context.Context, netexecutor.PrivacyEnvelopePlan) (bool, error) {
	return e.present, nil
}

func (e *v0239TerminalRecoveryExecutor) Apply(context.Context, netexecutor.PrivacyEnvelopePlan) error {
	return errors.New("terminal v0.2.39 recovery must not recreate Privacy Envelope")
}

func (e *v0239TerminalRecoveryExecutor) Replace(context.Context, netexecutor.PrivacyEnvelopePlan, netexecutor.PrivacyEnvelopePlan) error {
	return errors.New("terminal v0.2.39 recovery must not replace Privacy Envelope")
}

func (e *v0239TerminalRecoveryExecutor) Verify(ctx context.Context, plan netexecutor.PrivacyEnvelopePlan) error {
	return (netexecutor.PrivacyEnvelopeExecutor{Runner: v0239PrivacyEnvelopeRunner{drift: e.drift}}).Verify(ctx, plan)
}

func (e *v0239TerminalRecoveryExecutor) Remove(ctx context.Context, plan netexecutor.PrivacyEnvelopePlan) error {
	if err := e.Verify(ctx, plan); err != nil {
		return err
	}
	state, exists, err := e.store.Load()
	if err != nil {
		return err
	}
	if exists && state.Protection != nil && state.Protection.State == networkSessionProtectionRemoving && state.Protection.CompositionVersion == privacyEnvelopeCompositionVersion {
		e.sawCompositionV1Removing = true
	}
	e.removeCalls++
	e.removedFamily = plan.Family
	e.removedTable = plan.Table
	e.present = false
	return nil
}

type v0239PrivacyEnvelopeRunner struct{ drift bool }

func (r v0239PrivacyEnvelopeRunner) Run(_ context.Context, name string, args ...string) (netexecutor.CommandResult, error) {
	if name != "nft" || !reflect.DeepEqual(args, []string{"-j", "list", "table", "inet", "podlaz_pe_001122334455"}) {
		return netexecutor.CommandResult{ExitCode: 1}, errors.New("unexpected command")
	}
	output := v0239CanonicalPrivacyEnvelopeJSON()
	if r.drift {
		output = strings.Replace(output, `"right":"192.0.2.10"`, `"right":"198.51.100.20"`, 1)
	}
	return netexecutor.CommandResult{Stdout: output}, nil
}

func v0239CanonicalPrivacyEnvelopeJSON() string {
	return `{"nftables":[
{"metainfo":{"version":"1.0.9","release_name":"Old Doc Yak","json_schema_version":1}},
{"table":{"family":"inet","name":"podlaz_pe_001122334455","handle":10}},
{"chain":{"family":"inet","table":"podlaz_pe_001122334455","name":"output","handle":1,"type":"filter","hook":"output","prio":-10,"policy":"accept"}},
{"rule":{"family":"inet","table":"podlaz_pe_001122334455","chain":"output","handle":1,"expr":[{"match":{"op":"==","left":{"meta":{"key":"oifname"}},"right":"lo"}},{"counter":{"packets":0,"bytes":0}},{"accept":null}],"comment":"podlaz:privacy-envelope:loopback"}},
{"rule":{"family":"inet","table":"podlaz_pe_001122334455","chain":"output","handle":2,"expr":[{"match":{"op":"==","left":{"meta":{"key":"oifname"}},"right":"podlaz0"}},{"counter":{"packets":0,"bytes":0}},{"accept":null}],"comment":"podlaz:privacy-envelope:tun-egress"}},
{"rule":{"family":"inet","table":"podlaz_pe_001122334455","chain":"output","handle":3,"expr":[{"match":{"op":"==","left":{"meta":{"key":"nfproto"}},"right":"ipv4"}},{"match":{"op":"==","left":{"payload":{"protocol":"ip","field":"daddr"}},"right":"192.0.2.10"}},{"counter":{"packets":0,"bytes":0}},{"accept":null}],"comment":"podlaz:privacy-envelope:bootstrap"}},
{"rule":{"family":"inet","table":"podlaz_pe_001122334455","chain":"output","handle":4,"expr":[{"match":{"op":"==","left":{"meta":{"key":"nfproto"}},"right":"ipv4"}},{"match":{"op":"==","left":{"meta":{"key":"l4proto"}},"right":"udp"}},{"match":{"op":"==","left":{"payload":{"protocol":"udp","field":"sport"}},"right":68}},{"match":{"op":"==","left":{"payload":{"protocol":"udp","field":"dport"}},"right":67}},{"counter":{"packets":0,"bytes":0}},{"accept":null}],"comment":"podlaz:privacy-envelope:dhcp4"}},
{"rule":{"family":"inet","table":"podlaz_pe_001122334455","chain":"output","handle":5,"expr":[{"match":{"op":"==","left":{"meta":{"key":"nfproto"}},"right":"ipv6"}},{"match":{"op":"==","left":{"meta":{"key":"l4proto"}},"right":"udp"}},{"match":{"op":"==","left":{"payload":{"protocol":"udp","field":"sport"}},"right":546}},{"match":{"op":"==","left":{"payload":{"protocol":"udp","field":"dport"}},"right":547}},{"counter":{"packets":0,"bytes":0}},{"accept":null}],"comment":"podlaz:privacy-envelope:dhcp6"}},
{"rule":{"family":"inet","table":"podlaz_pe_001122334455","chain":"output","handle":6,"expr":[{"match":{"op":"==","left":{"meta":{"key":"l4proto"}},"right":"ipv6-icmp"}},{"match":{"op":"==","left":{"payload":{"protocol":"icmpv6","field":"type"}},"right":{"set":["nd-router-solicit","nd-neighbor-solicit","nd-neighbor-advert"]}}},{"counter":{"packets":0,"bytes":0}},{"accept":null}],"comment":"podlaz:privacy-envelope:ipv6-link-control"}},
{"rule":{"family":"inet","table":"podlaz_pe_001122334455","chain":"output","handle":7,"expr":[{"counter":{"packets":0,"bytes":0}},{"reject":{"type":"icmpx","expr":"port-unreachable"}}],"comment":"podlaz:privacy-envelope:block-direct"}}
]}`
}
