package daemon

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
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

	shutdownCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGUSR1)
	defer signal.Stop(signals)

	var intentMu sync.RWMutex
	intent := daemonapi.ShutdownStop
	setIntent := func(next daemonapi.ShutdownIntent) {
		intentMu.Lock()
		intent = next
		intentMu.Unlock()
	}
	shutdownIntent := func() daemonapi.ShutdownIntent {
		intentMu.RLock()
		defer intentMu.RUnlock()
		return intent
	}

	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case sig := <-signals:
			setIntent(shutdownIntentForSignal(sig))
			cancel()
		case <-ctx.Done():
			setIntent(daemonapi.ShutdownStop)
			cancel()
		case <-watchDone:
		}
	}()

	fmt.Fprintln(stdout, "podlazd: daemon started")
	fmt.Fprintln(stdout, "podlazd: serving local status API over Unix socket")
	fmt.Fprintln(stdout, "podlazd: network changes are applied only through daemon-owned transactions")

	if err := (daemonapi.Server{ShutdownIntent: shutdownIntent}).Run(shutdownCtx); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "podlazd: shutdown complete")
	return nil
}

func shutdownIntentForSignal(sig os.Signal) daemonapi.ShutdownIntent {
	if sig == syscall.SIGUSR1 {
		return daemonapi.ShutdownRestart
	}
	return daemonapi.ShutdownStop
}
