package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// The NM binary is deliberately separate from the default Xray executable.
// Package verification pins the official release and validates its checksum.
const nmOwnedPinnedXrayPath = "/usr/lib/podlaz/xray-nm-owned"

var errNMOwnedXrayIncompatible = errors.New("profile is incompatible with the official NetworkManager Xray runtime")

// preflightNMOwnedXrayCompatibility validates the fully composed, unmodified
// provider config with the exact packaged NM Xray *before any network apply*.
// It does not mutate networking, create TUN links or initiate FD handoff.
// No format-based allowlist can replace the official runtime's validation.
func preflightNMOwnedXrayCompatibility(ctx context.Context, config []byte) error {
	return preflightNMOwnedXrayCompatibilityWithRuntime(ctx, nmOwnedPinnedXrayPath, config)
}

// The runtime path is injectable only for executable contract tests; product
// callers must use preflightNMOwnedXrayCompatibility and the pinned binary.
func preflightNMOwnedXrayCompatibilityWithRuntime(ctx context.Context, xrayPath string, config []byte) error {
	if ctx == nil {
		return errors.New("NetworkManager Xray preflight requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !filepath.IsAbs(xrayPath) || len(config) == 0 {
		return nmOwnedXrayCompatibilityError()
	}
	dir, err := os.MkdirTemp("", "podlaz-nm-xray-compat-*")
	if err != nil {
		return fmt.Errorf("create private NM Xray preflight directory: %w", err)
	}
	defer os.RemoveAll(dir)

	err = preflightXrayTunSupport(ctx, xrayPath, filepath.Join(dir, "xray.json"), config, sameUserCoreExecutionIdentity())
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if err != nil {
		// Never return Xray output: it can contain imported server addresses,
		// security material, or provider-specific fields. Do not rewrite the
		// source profile, fall back, or try another runtime version.
		return nmOwnedXrayCompatibilityError()
	}
	return nil
}

func nmOwnedXrayCompatibilityError() error {
	return fmt.Errorf("%w; use the standard full-TUN backend or select an NM-compatible profile; no network changes were applied", errNMOwnedXrayIncompatible)
}
