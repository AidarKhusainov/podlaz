package daemon

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	daemonapi "github.com/AidarKhusainov/podlaz/internal/daemon"
)

// Run starts the privileged daemon skeleton.
//
// The daemon owns privileged networking mutations. Keeping that responsibility
// out of the user CLI avoids SUID binaries and makes crash recovery testable
// through one long-running process.
func Run(ctx context.Context, args []string) error {
	return run(ctx, args, os.Stdout)
}

func run(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) > 0 {
		return fmt.Errorf("podlazd does not accept arguments yet")
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Fprintln(stdout, "podlazd: daemon started")
	fmt.Fprintln(stdout, "podlazd: serving local status API over Unix socket")
	fmt.Fprintln(stdout, "podlazd: network changes are applied only through daemon-owned transactions")

	if err := (daemonapi.Server{}).Run(ctx); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "podlazd: shutdown complete")
	return nil
}
