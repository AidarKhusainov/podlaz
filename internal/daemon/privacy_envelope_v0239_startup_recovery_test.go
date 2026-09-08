package daemon

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestV0239PrivacyEnvelopeCrossesRestartThroughStartupRecovery(t *testing.T) {
	tests := []struct {
		name        string
		drift       bool
		wantErr     bool
		wantRemoved bool
	}{
		{name: "exact old state converges", wantRemoved: true},
		{name: "genuine drift stays fail closed", drift: true, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimeDir := t.TempDir()

			// Persist the affected-release shaped Network Session before the
			// simulated daemon restart. The fixed daemon below must discover this
			// state from disk rather than receiving an in-memory lifecycle object.
			beforeRestart := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
			if err := beforeRestart.Save(testContinuationRequest()); err != nil {
				t.Fatalf("persist pre-restart Network Session: %v", err)
			}
			protection := testArmedPrivacyProtection()
			if err := beforeRestart.stateStore().SetProtection(&protection); err != nil {
				t.Fatalf("persist v0.2.39 composition-v1 protection: %v", err)
			}
			if err := beforeRestart.stateStore().SetIntent(networkSessionIntentTerminal); err != nil {
				t.Fatalf("persist terminal intent before restart: %v", err)
			}

			// A new continuation/store represents the fixed daemon process after
			// package replacement or restart on the same boot.
			afterRestart := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
			runner := &v0239StartupRecoveryRunner{present: true, drift: tt.drift, generation: 77}
			afterRestart.recoverExact = func(context.Context, string) api.RecoveryResponse {
				return api.RecoveryResponse{Mode: "execute"}
			}
			afterRestart.continueTeardown = func(ctx context.Context, store networkSessionStateStore) error {
				return continuePersistedNetworkSessionTeardownWith(
					ctx,
					store,
					netexecutor.PrivacyEnvelopeExecutor{Runner: runner},
					func(context.Context) error { return nil },
				)
			}

			resumed, err := resumeNetworkSession(
				context.Background(),
				afterRestart,
				networkSessionRecordingLifecycle{events: &[]string{}},
				func(context.Context) api.StatusResponse { return api.StatusResponse{Connection: "inactive"} },
				func(context.Context, api.StatusResponse) api.RecoveryResponse {
					return api.RecoveryResponse{Mode: "execute"}
				},
			)
			if tt.wantErr {
				if err == nil || resumed {
					t.Fatalf("drifted startup recovery must remain incomplete: resumed=%v err=%v", resumed, err)
				}
			} else {
				if err != nil || resumed {
					t.Fatalf("terminal startup recovery: resumed=%v err=%v", resumed, err)
				}
			}

			if tt.wantRemoved {
				if runner.removeCalls != 1 {
					t.Fatalf("generation-guarded removal calls=%d, want 1", runner.removeCalls)
				}
				if runner.removedFamily != protection.Family || runner.removedTable != protection.Table || runner.removedHandle != 10 || runner.removedGeneration != 77 {
					t.Fatalf("unexpected exact removal target: family=%q table=%q handle=%d generation=%d", runner.removedFamily, runner.removedTable, runner.removedHandle, runner.removedGeneration)
				}
				if runner.absenceObservations == 0 {
					t.Fatal("startup teardown cleared authority without structured post-delete absence observation")
				}
				if _, exists, loadErr := afterRestart.stateStore().Load(); loadErr != nil || exists {
					t.Fatalf("converged startup recovery must clear Network Session authority: exists=%v err=%v", exists, loadErr)
				}
				return
			}

			if runner.removeCalls != 0 {
				t.Fatalf("drifted live table must never be mutated, remove calls=%d", runner.removeCalls)
			}
			state, exists, loadErr := afterRestart.stateStore().Load()
			if loadErr != nil || !exists || state.Protection == nil {
				t.Fatalf("failed startup recovery lost durable protection authority: exists=%v state=%#v err=%v", exists, state, loadErr)
			}
			if state.Protection.State != networkSessionProtectionArmed || state.Protection.CompositionVersion != privacyEnvelopeCompositionVersion {
				t.Fatalf("failed startup recovery changed protection authority: %#v", state.Protection)
			}
		})
	}
}

type v0239StartupRecoveryRunner struct {
	present             bool
	drift               bool
	generation          uint32
	removeCalls         int
	absenceObservations int
	removedFamily       string
	removedTable        string
	removedHandle       uint64
	removedGeneration   uint32
}

func (r *v0239StartupRecoveryRunner) Run(_ context.Context, name string, args ...string) (netexecutor.CommandResult, error) {
	if name != "nft" {
		return netexecutor.CommandResult{ExitCode: 1}, fmt.Errorf("unexpected command %q", name)
	}
	if reflect.DeepEqual(args, []string{"-j", "list", "tables"}) {
		if !r.present {
			r.absenceObservations++
			return netexecutor.CommandResult{Stdout: `{"nftables":[{"metainfo":{"version":"1.0.9","release_name":"Old Doc Yak","json_schema_version":1}}]}`}, nil
		}
		return netexecutor.CommandResult{Stdout: `{"nftables":[{"metainfo":{"version":"1.0.9","release_name":"Old Doc Yak","json_schema_version":1}},{"table":{"family":"inet","name":"podlaz_pe_001122334455","handle":10}}]}`}, nil
	}
	if reflect.DeepEqual(args, []string{"-j", "list", "table", "inet", "podlaz_pe_001122334455"}) {
		if !r.present {
			return netexecutor.CommandResult{ExitCode: 1, Stderr: "No such file or directory"}, errors.New("nft table absent")
		}
		output := v0239CanonicalPrivacyEnvelopeJSON()
		if r.drift {
			output = strings.Replace(output, `"right":"192.0.2.10"`, `"right":"198.51.100.20"`, 1)
		}
		return netexecutor.CommandResult{Stdout: output}, nil
	}
	return netexecutor.CommandResult{ExitCode: 1}, fmt.Errorf("unexpected nft args: %#v", args)
}

func (r *v0239StartupRecoveryRunner) NftablesGeneration(context.Context) (uint32, error) {
	return r.generation, nil
}

func (r *v0239StartupRecoveryRunner) NftablesRemoveTable(_ context.Context, family, table string, handle uint64, generation uint32) error {
	if !r.present {
		return errors.New("attempted to remove an already absent table")
	}
	r.removeCalls++
	r.removedFamily = family
	r.removedTable = table
	r.removedHandle = handle
	r.removedGeneration = generation
	r.present = false
	return nil
}

func (r *v0239StartupRecoveryRunner) NftablesReplaceTable(context.Context, string, string, uint64, uint32, planner.TunFirewallPlan) error {
	return errors.New("terminal v0.2.39 startup recovery must not replace Privacy Envelope")
}
