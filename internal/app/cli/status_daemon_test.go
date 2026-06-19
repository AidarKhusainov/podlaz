package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/client"
	"github.com/AidarKhusainov/podlaz/internal/status"
)

func TestRunCLIStatusUsesDaemonWhenReachable(t *testing.T) {
	var out bytes.Buffer

	err := runWithOptions(context.Background(), []string{"status"}, &out, options{
		daemonStatus: func(context.Context) (status.Report, error) {
			return status.Report{
				Daemon:           "running",
				Service:          "systemd",
				Connection:       "inactive",
				RuntimeDirectory: status.RuntimeDirectory{Message: "present"},
				Proxy:            "inactive",
				TUN:              "disabled",
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}

	got := out.String()
	for _, want := range []string{"Daemon: running", "Service: systemd", "Connection: inactive", "Proxy: inactive", "TUN: disabled"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in %q", want, got)
		}
	}
}

func TestRunCLIStatusFallsBackWhenDaemonUnavailable(t *testing.T) {
	var out bytes.Buffer

	err := runWithOptions(context.Background(), []string{"status"}, &out, options{
		daemonStatus: func(context.Context) (status.Report, error) {
			return status.Report{}, client.ErrDaemonUnavailable
		},
	})
	if err != nil {
		t.Fatalf("unavailable daemon with clean fallback should not fail: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "Daemon: not reachable") || !strings.Contains(got, "using local fallback") {
		t.Fatalf("expected clear fallback output, got %q", got)
	}
}

func TestRunCLIStatusWarnsWhenDaemonProtocolFails(t *testing.T) {
	var out bytes.Buffer

	err := runWithOptions(context.Background(), []string{"status"}, &out, options{
		daemonStatus: func(context.Context) (status.Report, error) {
			return status.Report{}, errors.New("bad daemon response")
		},
	})
	if err == nil {
		t.Fatal("expected protocol failure to return diagnostic exit")
	}
	if got := ExitCode(err); got != 3 {
		t.Fatalf("expected exit 3, got %d", got)
	}
	if got := out.String(); !strings.Contains(got, "could not inspect daemon status API") {
		t.Fatalf("expected daemon warning, got %q", got)
	}
}
