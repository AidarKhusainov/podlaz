package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/AidarKhusainov/tunwarden/internal/recovery"
)

func runRecoverCommand(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	if isHelp(args) {
		printRecoverHelp(stdout)
		return nil
	}
	if len(args) > 0 {
		if contains(args, "--execute") {
			return usageError("recover --execute is not implemented in v0.1")
		}
		return usageError("unsupported recover argument %q", args[0])
	}

	plan := runRecover(ctx, opts)
	fmt.Fprint(stdout, plan.String())
	return nil
}

func runRecover(ctx context.Context, opts options) recovery.PlanResult {
	if opts.recover != nil {
		return opts.recover(ctx)
	}
	return recovery.Plan(ctx)
}
