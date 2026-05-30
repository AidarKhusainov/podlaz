package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// RunDaemon starts the privileged daemon skeleton.
//
// The daemon will eventually own all privileged networking mutations. Keeping
// that responsibility out of the user CLI avoids SUID binaries and makes crash
// recovery testable through one long-running process.
func RunDaemon(ctx context.Context, _ []string) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Println("tunwardend: daemon skeleton started")
	fmt.Println("tunwardend: no network changes are applied in this build")

	<-ctx.Done()
	fmt.Println("tunwardend: shutdown requested")
	return nil
}
