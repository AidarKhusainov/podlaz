//go:build linux

package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/AidarKhusainov/podlaz/internal/engine"
	"github.com/AidarKhusainov/podlaz/internal/profile"
)

// nmOwnedXrayPrepared contains the fully composed and official-binary-tested
// configuration. The first phase has no network/Privacy Envelope side effects.
// A digest and exact interface/MTU/mark prevent substituting different data
// between this read-only preflight and the later protected NM staging phase.
type nmOwnedXrayPrepared struct {
	config        []byte
	digest        [32]byte
	interfaceName string
	mtu           int
	mark          uint32
}

// prepareNMOwnedXray is the pre-mutation boundary. It MUST be invoked before
// arming nftables, changing routes/DNS, or requesting NM activation, not only
// before passing an inherited TUN FD. No profile heuristics replace the
// pinned, unmodified official Xray -test over the complete generated config.
func prepareNMOwnedXray(
	ctx context.Context, p profile.Profile, opts engine.XrayTunConfigOptions,
) (*nmOwnedXrayPrepared, error) {
	return prepareNMOwnedXrayWith(ctx, p, opts,
		engine.GenerateNMOwnedXrayTunConfig,
		preflightNMOwnedXrayCompatibility)
}

func prepareNMOwnedXrayWith(
	ctx context.Context, p profile.Profile, opts engine.XrayTunConfigOptions,
	generate func(profile.Profile, engine.XrayTunConfigOptions) ([]byte, error),
	preflight func(context.Context, []byte) error,
) (*nmOwnedXrayPrepared, error) {
	if ctx == nil || generate == nil || preflight == nil ||
		opts.Name == "" || opts.MTU <= 0 || opts.EgressMark == 0 {
		return nil, errors.New("NM Xray pre-mutation compatibility requires exact interface, MTU and egress mark")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	generated, err := generate(p, opts)
	if err != nil {
		return nil, err
	}
	if len(generated) == 0 {
		return nil, errors.New("NM Xray generator returned empty config")
	}
	if err := preflight(ctx, generated); err != nil {
		return nil, fmt.Errorf("official NM Xray compatibility check failed before any network mutation: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	config := append([]byte(nil), generated...)
	return &nmOwnedXrayPrepared{
		config: config, digest: nmXrayAuthorityDigest(config, opts.Name, opts.MTU, opts.EgressMark),
		interfaceName: opts.Name, mtu: opts.MTU, mark: opts.EgressMark,
	}, nil
}

// stagePreparedNMOwnedXray may be invoked only after the normal protected
// Network Session has armed its exact marked Privacy Envelope. It never
// retries a failed D-Bus operation or interprets observations as cleanup
// authority. It retains partially persisted NM intent on ambiguous failure.
func (s nmL3Stager) stagePreparedNMOwnedXray(
	ctx context.Context, proof *nmOwnedXrayPrepared, sessionID string, plan nmL3Plan,
) ([]byte, nmL3Intent, error) {
	return stagePreparedNMOwnedXrayWith(ctx, proof, sessionID, plan, s.StageProtected)
}

func stagePreparedNMOwnedXrayWith(
	ctx context.Context, proof *nmOwnedXrayPrepared, sessionID string,
	plan nmL3Plan, stage func(context.Context, string, uint32, nmL3Plan) (nmL3Intent, error),
) ([]byte, nmL3Intent, error) {
	if ctx == nil || proof == nil || stage == nil ||
		!networkSessionIDPattern.MatchString(sessionID) ||
		proof.interfaceName != plan.Interface || proof.mtu != int(plan.MTU) ||
		proof.mark == 0 || len(proof.config) == 0 ||
		nmXrayAuthorityDigest(proof.config, proof.interfaceName, proof.mtu, proof.mark) != proof.digest {
		return nil, nmL3Intent{}, errors.New("NM Xray preflight proof changed or has inconsistent L3 authority")
	}
	if err := ctx.Err(); err != nil {
		return nil, nmL3Intent{}, err
	}
	if err := validateNML3Plan(plan); err != nil {
		return nil, nmL3Intent{}, fmt.Errorf("invalid protected NM L3 plan: %w", err)
	}
	intent, err := stage(ctx, sessionID, proof.mark, plan)
	if err != nil {
		return nil, intent, err
	}
	return append([]byte(nil), proof.config...), intent, nil
}

// nmXrayAuthorityDigest seals the *tested* exact config together with
// generation admission choices. A mutated mark is otherwise invisible to a
// digest of JSON alone and can divert marked outbound transport.
// Length-delimited fields and fixed-width integers avoid ambiguous encoding.
func nmXrayAuthorityDigest(config []byte, name string, mtu int, mark uint32) [32]byte {
	h := sha256.New()
	var encoded [8]byte
	binary.BigEndian.PutUint32(encoded[:4], uint32(len(name)))
	_, _ = h.Write(encoded[:4])
	_, _ = h.Write([]byte(name))
	binary.BigEndian.PutUint32(encoded[:4], uint32(mtu))
	binary.BigEndian.PutUint32(encoded[4:], mark)
	_, _ = h.Write(encoded[:])
	binary.BigEndian.PutUint32(encoded[:4], uint32(len(config)))
	_, _ = h.Write(encoded[:4])
	_, _ = h.Write(config)
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result
}

func (p *nmOwnedXrayPrepared) configSHA256() string {
	if p == nil || nmXrayAuthorityDigest(p.config, p.interfaceName, p.mtu, p.mark) != p.digest {
		return ""
	}
	sum := sha256.Sum256(p.config)
	return hex.EncodeToString(sum[:])
}
