//go:build linux

package daemon

import (
	"context"
	"errors"
	"fmt"
)

// verifyNMOwnedTunReadOnlyPreflight coordinates two independent observations.
// It must never be used as admission authority without durable generation
// fencing, a privileged broker, complete confinement and armed protection.
func verifyNMOwnedTunReadOnlyPreflight(ctx context.Context, fd int, kernel nmTunFDKernelIdentity, nm nmTunDBusAuthority) error {
	return verifyNMOwnedTunReadOnlyPreflightWith(ctx, fd, kernel, nm, verifyNMOwnedTunFDKernelIdentity, verifyNMTunDBusAuthority)
}

func verifyNMOwnedTunReadOnlyPreflightWith(
	ctx context.Context, fd int, kernel nmTunFDKernelIdentity, nm nmTunDBusAuthority,
	kernelCheck func(int, nmTunFDKernelIdentity) error,
	nmCheck func(context.Context, nmTunDBusAuthority) error,
) error {
	if ctx == nil || fd < 3 || kernelCheck == nil || nmCheck == nil ||
		kernel.Name == "" || kernel.Name != nm.Name || kernel.MTU <= 0 ||
		uint64(kernel.MTU) != uint64(nm.MTU) {
		return errors.New("incomplete NM-owned TUN preflight authority")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := kernelCheck(fd, kernel); err != nil {
		return fmt.Errorf("NM-owned TUN preflight initial FD identity: %w", err)
	}
	if err := nmCheck(ctx, nm); err != nil {
		return fmt.Errorf("NM-owned TUN preflight active D-Bus authority: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := kernelCheck(fd, kernel); err != nil {
		return fmt.Errorf("NM-owned TUN preflight repeated FD identity: %w", err)
	}
	return ctx.Err()
}
