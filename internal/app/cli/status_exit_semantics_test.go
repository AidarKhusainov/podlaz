package cli

import (
	"bytes"
	"context"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/status"
)

func TestStatusCommandExitContractDistinguishesLifecycleAndWarningTypes(t *testing.T) {
	tests := []struct {
		name     string
		report   status.Report
		wantCode int
	}{
		{
			name: "healthy active runtime warning",
			report: status.Report{
				Daemon:           "running",
				Service:          "systemd",
				Connection:       "active",
				RuntimeDirectory: status.RuntimeDirectory{Message: "present"},
				Proxy:            "active",
				TUN:              "active",
				RuntimeWarnings:  []string{"configured TUN MTU differs from the physical path"},
			},
			wantCode: 0,
		},
		{
			name: "core exited",
			report: status.Report{
				Daemon:           "running",
				Service:          "systemd",
				Connection:       "error (core exited)",
				RuntimeDirectory: status.RuntimeDirectory{Message: "present"},
				Proxy:            "inactive",
				TUN:              "disabled",
				RuntimeWarnings:  []string{"Xray process exited unexpectedly"},
			},
			wantCode: 3,
		},
		{
			name: "inspection failure",
			report: status.Report{
				Daemon:           "running",
				Service:          "systemd",
				Connection:       "active",
				RuntimeDirectory: status.RuntimeDirectory{Message: "present"},
				Proxy:            "active",
				TUN:              "active",
				Warnings: []status.Warning{{
					Target:  "transaction state",
					Message: "cannot inspect transaction fixture",
				}},
			},
			wantCode: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := runStatusCommand(context.Background(), nil, &out, options{
				status: func(context.Context) status.Report { return tt.report },
			})
			if got := ExitCode(err); got != tt.wantCode {
				t.Fatalf("unexpected status exit code: got %d want %d, err=%v, output=%q", got, tt.wantCode, err, out.String())
			}
		})
	}
}
