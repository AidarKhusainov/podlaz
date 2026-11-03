//go:build linux

package daemon

import (
	"context"
	"errors"
	"fmt"

	"github.com/AidarKhusainov/podlaz/internal/engine"
	"github.com/AidarKhusainov/podlaz/internal/profile"
)

// stageNMOwnedXray prepares an NM-owned TUN data plane but does not activate
// the user-facing NM backend. It is a single semantic boundary: the pinned
// official Xray -test MUST succeed on the complete generated native/provider
// config before the first NetworkManager L3 or firewall mutation.
// The caller must already have established an armed protected Network Session.
func (s nmL3Stager) stageNMOwnedXray(
	ctx context.Context, p profile.Profile, sessionID string, mark uint32,
	opts engine.XrayTunConfigOptions, plan nmL3Plan,
) ([]byte, nmL3Intent, error) {
	return stageNMOwnedXrayWith(ctx, p, sessionID, mark, opts, plan,
		engine.GenerateNMOwnedXrayTunConfig,
		preflightNMOwnedXrayCompatibility,
		s.StageProtected)
}

func stageNMOwnedXrayWith(
	ctx context.Context, p profile.Profile, sessionID string, mark uint32,
	opts engine.XrayTunConfigOptions, plan nmL3Plan,
	generate func(profile.Profile, engine.XrayTunConfigOptions) ([]byte, error),
	preflight func(context.Context, []byte) error,
	stage func(context.Context, string, uint32, nmL3Plan) (nmL3Intent, error),
) ([]byte, nmL3Intent, error) {
	if ctx == nil || generate == nil || preflight == nil || stage == nil ||
		mark == 0 || !networkSessionIDPattern.MatchString(sessionID) ||
		opts.Name != plan.Interface || opts.MTU != int(plan.MTU) ||
		opts.EgressMark != mark {
		return nil, nmL3Intent{}, errors.New("NM Xray config and L3 plan have inconsistent admission authority")
	}
	if err := ctx.Err(); err != nil {
		return nil, nmL3Intent{}, err
	}
	if err := validateNML3Plan(plan); err != nil {
		return nil, nmL3Intent{}, fmt.Errorf("invalid protected NM plan: %w", err)
	}
	generated, err := generate(p, opts)
	if err != nil {
		return nil, nmL3Intent{}, err
	}
	if len(generated) == 0 {
		return nil, nmL3Intent{}, errors.New("NM Xray generator returned empty config")
	}
	if err := preflight(ctx, generated); err != nil {
		return nil, nmL3Intent{}, fmt.Errorf("official NM Xray compatibility check failed before network mutation: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, nmL3Intent{}, err
	}
	intent, err := stage(ctx, sessionID, mark, plan)
	if err != nil {
		// Never discard exact partially persisted NM mutation authority.
		// A timed-out D-Bus call can succeed after the caller returns.
		return nil, intent, err
	}
	return generated, intent, nil
}
