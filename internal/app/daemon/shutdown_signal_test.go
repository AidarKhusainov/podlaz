package daemon

import (
	"os"
	"syscall"
	"testing"

	daemonapi "github.com/AidarKhusainov/podlaz/internal/daemon"
)

func TestShutdownIntentForSignal(t *testing.T) {
	tests := []struct {
		name string
		sig  os.Signal
		want daemonapi.ShutdownIntent
	}{
		{name: "restart", sig: syscall.SIGUSR1, want: daemonapi.ShutdownRestart},
		{name: "service stop", sig: syscall.SIGTERM, want: daemonapi.ShutdownStop},
		{name: "interactive stop", sig: os.Interrupt, want: daemonapi.ShutdownStop},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shutdownIntentForSignal(tt.sig); got != tt.want {
				t.Fatalf("shutdownIntentForSignal(%v) = %q, want %q", tt.sig, got, tt.want)
			}
		})
	}
}
