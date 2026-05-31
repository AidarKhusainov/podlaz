package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/AidarKhusainov/tunwarden/internal/doctor"
	"github.com/AidarKhusainov/tunwarden/internal/recover"
)

const version = "0.0.0-dev"

// RunCLI executes the user-facing TunWarden command line interface.
func RunCLI(ctx context.Context, args []string) error {
	return runCLI(ctx, args, os.Stdout)
}

func runCLI(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		printUsage(stdout)
		return nil
	}

	switch strings.ToLower(args[0]) {
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	case "version", "--version":
		fmt.Fprintf(stdout, "tunwarden %s\n", version)
		return nil
	case "doctor":
		report := doctor.Run(ctx)
		fmt.Fprint(stdout, report.String())
		if report.HasFailures() {
			return errors.New("doctor found failing checks")
		}
		return nil
	case "recover":
		plan := recover.Plan()
		fmt.Fprint(stdout, plan.String())
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `TunWarden - Linux-first safe TUN VPN client foundation

Usage:
  tunwarden version
  tunwarden doctor
  tunwarden recover

Current status:
  This is an early foundation build. Commands print contracts and diagnostic
  plans; they do not yet mutate system networking state.
`)
}
