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

func TestNMOfficialXrayCompatibilityPrecedesEveryNetworkManagerWrite(t *testing.T) {
	plan := testNML3Plan()
	opts := engine.XrayTunConfigOptions{
		Name: plan.Interface, MTU: int(plan.MTU), EgressMark: 101,
	}
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
			marker := errors.New("sensitive runtime rejection")
			stageErr := errors.New("ambiguous D-Bus mutation")
			source := []byte(`{"inbounds":[]}`)
			cfg, intent, err := stageNMOwnedXrayWith(
				context.Background(), profile.Profile{}, "00112233445566778899aabbccddeeff",
				101, opts, plan,
				func(profile.Profile, engine.XrayTunConfigOptions) ([]byte, error) {
					steps = append(steps, "generate")
					return source, nil
				},
				func(context.Context, []byte) error {
					steps = append(steps, "official-preflight")
					if tc.preflightError {
						return marker
					}
					return nil
				},
				func(context.Context, string, uint32, nmL3Plan) (nmL3Intent, error) {
					steps = append(steps, "NM-stage")
					if tc.stageError {
						return nmL3Intent{Stage: "planned"}, stageErr
					}
					return nmL3Intent{Stage: "activating"}, nil
				},
			)
			expected := []string{"generate", "official-preflight", "NM-stage"}
			if tc.preflightError {
				expected = expected[:2]
				if !errors.Is(err, marker) || len(cfg) != 0 || intent.Stage != "" {
					t.Fatalf("incompatible config reached NM or returned authority: %v", err)
				}
			} else if tc.stageError {
				if !errors.Is(err, stageErr) || len(cfg) != 0 || intent.Stage != "planned" {
					t.Fatalf("ambiguous NM mutation was erased: %v %#v", err, intent)
				}
			} else if err != nil || intent.Stage != "activating" || string(cfg) != string(source) {
				t.Fatalf("accepted config changed: %v %#v", err, intent)
			}
			if !reflect.DeepEqual(steps, expected) {
				t.Fatalf("unsafe NM/official-Xray order: %v, expected %v", steps, expected)
			}
		})
	}
}

func TestNMOfficialXrayRejectsPlanDriftWithoutEvenGeneratingConfig(t *testing.T) {
	plan := testNML3Plan()
	called := false
	generate := func(profile.Profile, engine.XrayTunConfigOptions) ([]byte, error) {
		called = true
		return []byte("{}"), nil
	}
	preflight := func(context.Context, []byte) error { called = true; return nil }
	stage := func(context.Context, string, uint32, nmL3Plan) (nmL3Intent, error) {
		called = true
		return nmL3Intent{}, nil
	}
	for _, tc := range []engine.XrayTunConfigOptions{
		{Name: "foreign0", MTU: int(plan.MTU), EgressMark: 101},
		{Name: plan.Interface, MTU: 1300, EgressMark: 101},
		{Name: plan.Interface, MTU: int(plan.MTU), EgressMark: 102},
	} {
		_, _, err := stageNMOwnedXrayWith(context.Background(), profile.Profile{},
			"00112233445566778899aabbccddeeff", 101, tc, plan, generate, preflight, stage)
		if err == nil || called {
			t.Fatalf("mismatched NM config reached generator or network mutation: %v", err)
		}
	}
}
