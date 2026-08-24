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
	if got := out.String(); got != "Status: Disconnected\n" {
		t.Fatalf("unexpected product status: %q", got)
	}
}

func TestRunCLIStatusFallsBackAsUnknownWhenDaemonUnavailable(t *testing.T) {
	var out bytes.Buffer

	err := runWithOptions(context.Background(), []string{"status"}, &out, options{
		daemonStatus: func(context.Context) (status.Report, error) {
			return status.Report{}, client.ErrDaemonUnavailable
		},
	})
	if err == nil || ExitCode(err) != 3 {
		t.Fatalf("unavailable daemon must return unknown exit 3, err=%v code=%d", err, ExitCode(err))
	}
	got := out.String()
	if !strings.Contains(got, "Status: Unknown") || strings.Contains(got, "Status: Disconnected") {
		t.Fatalf("unsafe fallback product status: %q", got)
	}
}

func TestRunCLIStatusWarnsSafelyWhenDaemonProtocolFails(t *testing.T) {
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
	got := out.String()
	if !strings.Contains(got, "Status: Unknown") || !strings.Contains(got, "Reason: Connection state could not be determined") {
		t.Fatalf("expected safe unknown status, got %q", got)
	}
}
