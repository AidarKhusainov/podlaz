package planner

import (
	"fmt"

	"github.com/AidarKhusainov/tunwarden/internal/engine"
	"github.com/AidarKhusainov/tunwarden/internal/profile"
)

// ValidateXrayConnectProfile checks whether the profile is supported by the
// daemon-owned Xray lifecycle for the selected connection mode. It is pure
// preflight validation and must not write runtime config or mutate host state.
func ValidateXrayConnectProfile(p profile.Profile, mode string) error {
	switch mode {
	case ModeProxyOnly:
		return engine.ValidateXrayProxyOnlyProfile(p)
	case ModeTun:
		return engine.ValidateXrayTunProfile(p)
	default:
		return fmt.Errorf("unsupported connect mode %q", mode)
	}
}
