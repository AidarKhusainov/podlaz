//go:build linux

package daemon

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/engine"
	"github.com/AidarKhusainov/podlaz/internal/profile"
)

func TestNMOfficialXrayPreflightIsSeparateFromEveryProtectedMutation(t *testing.T) {
	plan := testNML3Plan()
	opts := engine.XrayTunConfigOptions{Name: plan.Interface, MTU: int(plan.MTU), EgressMark: 101}
	for _, tc := range []struct {
		name           string
		preflightError bool
		stageError     bool
	}{
		{"compatible", false, false},
		{"official Xray rejects native provider config", true, false},
		{"NM activation ambiguous after successful compatibility", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var steps []string
			rejected := errors.New("official binary refused configuration")
			ambiguous := errors.New("ambiguous NetworkManager D-Bus mutation")
			raw := []byte(`{"inbounds":[]}`)
			prepared, err := prepareNMOwnedXrayWith(context.Background(), profile.Profile{}, opts,
				func(profile.Profile, engine.XrayTunConfigOptions) ([]byte, error) {
					steps = append(steps, "generate")
					return raw, nil
				},
				func(context.Context, []byte) error {
					steps = append(steps, "official-preflight")
					if tc.preflightError {
						return rejected
					}
					return nil
				})
			if tc.preflightError {
				if !errors.Is(err, rejected) || prepared != nil ||
					!reflect.DeepEqual(steps, []string{"generate", "official-preflight"}) {
					t.Fatalf("rejected config reached mutation phase: %v, steps=%v", err, steps)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			// A real caller must arm protection only here, after preflight.
			steps = append(steps, "arm-envelope")
			config, intent, err := stagePreparedNMOwnedXrayWith(context.Background(), prepared,
				"00112233445566778899aabbccddeeff", plan,
				func(context.Context, string, uint32, nmL3Plan) (nmL3Intent, error) {
					steps = append(steps, "NM-stage")
					if tc.stageError {
						return nmL3Intent{Stage: "planned"}, ambiguous
					}
					return nmL3Intent{Stage: "activating"}, nil
				})
			if tc.stageError {
				if !errors.Is(err, ambiguous) || len(config) != 0 || intent.Stage != "planned" {
					t.Fatalf("ambiguous NM intent discarded: %v %#v", err, intent)
				}
			} else if err != nil || string(config) != string(raw) || intent.Stage != "activating" {
				t.Fatalf("accepted official config changed: %v %#v", err, intent)
			}
			expected := []string{"generate", "official-preflight", "arm-envelope", "NM-stage"}
			if !reflect.DeepEqual(steps, expected) {
				t.Fatalf("unsafe official preflight/Privacy Envelope/NM order: %v", steps)
			}
		})
	}
}

func TestNMPreparedOfficialXrayConfigIsBoundToExactPlanAndUnmodifiedBytes(t *testing.T) {
	plan := testNML3Plan()
	opts := engine.XrayTunConfigOptions{Name: plan.Interface, MTU: int(plan.MTU), EgressMark: 101}
	original := []byte(`{"inbounds":[]}`)
	proof, err := prepareNMOwnedXrayWith(context.Background(), profile.Profile{}, opts,
		func(profile.Profile, engine.XrayTunConfigOptions) ([]byte, error) { return original, nil },
		func(context.Context, []byte) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	mutations := []struct {
		name string
		edit func(*nmOwnedXrayPrepared, *nmL3Plan)
	}{
		{"interface", func(p *nmOwnedXrayPrepared, _ *nmL3Plan) { p.interfaceName = "foreign0" }},
		{"MTU", func(p *nmOwnedXrayPrepared, _ *nmL3Plan) { p.mtu++ }},
		{"mark", func(p *nmOwnedXrayPrepared, _ *nmL3Plan) { p.mark++ }},
		{"payload", func(p *nmOwnedXrayPrepared, _ *nmL3Plan) { p.config[0] = 'x' }},
		{"plan", func(_ *nmOwnedXrayPrepared, p *nmL3Plan) { p.MTU-- }},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			clone := *proof
			clone.config = append([]byte(nil), proof.config...)
			changedPlan := plan
			tc.edit(&clone, &changedPlan)
			called := false
			_, _, err := stagePreparedNMOwnedXrayWith(context.Background(), &clone,
				"00112233445566778899aabbccddeeff", changedPlan,
				func(context.Context, string, uint32, nmL3Plan) (nmL3Intent, error) {
					called = true
					return nmL3Intent{}, nil
				})
			if err == nil || called {
				t.Fatalf("altered official Xray proof reached NM mutation: %v", err)
			}
		})
	}
}
