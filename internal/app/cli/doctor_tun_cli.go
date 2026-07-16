package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/AidarKhusainov/podlaz/internal/client"
	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

func runTunDoctorCommand(ctx context.Context, stdout io.Writer, opts options, args parsedDoctorArgs) error {
	report, err := runTunDoctor(ctx, opts)
	if err != nil {
		return err
	}
	if args.json {
		if err := tundiag.WriteJSON(stdout, report); err != nil {
			return fmt.Errorf("write TUN diagnostics JSON: %w", err)
		}
	} else {
		fmt.Fprint(stdout, tundiag.RenderHuman(report, args.verbose))
	}
	if report.HasFailures() {
		return exitError{code: 3, err: errors.New("doctor --tun found unhealthy diagnostics")}
	}
	return nil
}

func runTunDoctor(ctx context.Context, opts options) (tundiag.Report, error) {
	if opts.tunDoctor != nil {
		return opts.tunDoctor(ctx)
	}
	return (client.DoctorClient{}).TunDiagnostics(ctx)
}
