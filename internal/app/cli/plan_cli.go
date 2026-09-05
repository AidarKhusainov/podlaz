package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/engine"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/profile"
)

func runPlanCommand(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	if isHelp(args) {
		printPlanHelp(stdout)
		return nil
	}
	parsed, err := parsePlanArgs(args)
	if err != nil {
		return err
	}
	store, err := profile.NewStore(opts.profileStorePath)
	if err != nil {
		return err
	}
	p, err := store.Get(parsed.profileID)
	if err != nil {
		return profileCommandError(err)
	}
	if parsed.mode == planner.ModeProxyOnly {
		plan, err := planner.PlanProxyOnly(p)
		if err != nil {
			return usageError("%s", err.Error())
		}
		if parsed.jsonOutput {
			return writeJSON(stdout, proxyOnlyPlanJSON(plan))
		}
		renderProxyOnlyPlan(stdout, plan)
		return nil
	}
	if err := engine.ValidateXrayTunProfile(p); err != nil {
		return usageError("%s", err.Error())
	}

	collectSnapshot := opts.systemSnapshot
	if collectSnapshot == nil {
		collectSnapshot = netsnapshot.Collect
	}
	snapshot := collectSnapshot(ctx, netsnapshot.Options{
		Server:   p.Server,
		TunNames: []string{netsnapshot.DefaultTunName},
	})

	evidence, err := collectPlanTunAllocationEvidence(ctx, opts, snapshot)
	if err != nil {
		return usageError("%s", err.Error())
	}
	plan, err := planner.PlanTunForSessionWithAllocationEvidence(p, snapshot, evidence, planner.TunOptions{})
	if err != nil {
		return usageError("%s", err.Error())
	}
	if parsed.jsonOutput {
		return writeJSON(stdout, tunPlanJSON(plan))
	}
	if parsed.verbose {
		renderTunPlanVerbose(stdout, plan)
		return nil
	}
	renderTunPlanSummary(stdout, plan, parsed.profileID, parsed.plainOutput)
	return nil
}

func collectPlanTunAllocationEvidence(ctx context.Context, opts options, diagnostic netsnapshot.Snapshot) (netsnapshot.TunAllocationEvidence, error) {
	if opts.tunAllocationEvidence != nil {
		return opts.tunAllocationEvidence(ctx)
	}
	if opts.systemSnapshot != nil {
		// systemSnapshot is an unexported deterministic test seam. Production
		// read-only planning uses the same rtnetlink allocation authority as the
		// daemon and never falls back to presentation-oriented snapshot parsing.
		return netsnapshot.TunAllocationEvidenceFromSnapshot(diagnostic)
	}
	return netsnapshot.CollectTunAllocationEvidence(ctx)
}

type planArgs struct {
	mode        string
	profileID   string
	jsonOutput  bool
	verbose     bool
	plainOutput bool
}

func parsePlanArgs(args []string) (planArgs, error) {
	var parsed planArgs
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value, hasInlineValue := cutFlagValue(arg)
		switch {
		case arg == "--mode" || strings.HasPrefix(arg, "--mode="):
			v, next, err := flagValue("plan --mode", args, i, value, hasInlineValue)
			if err != nil {
				return parsed, err
			}
			parsed.mode = strings.ToLower(strings.TrimSpace(v))
			i = next
		case arg == "--json":
			parsed.jsonOutput = true
		case arg == "--verbose" || arg == "-v":
			parsed.verbose = true
		case arg == "--plain":
			parsed.plainOutput = true
		default:
			if strings.HasPrefix(arg, "-") {
				return parsed, usageError("unsupported plan argument %q", arg)
			}
			if parsed.profileID != "" {
				return parsed, usageError("plan accepts exactly one profile id")
			}
			parsed.profileID = arg
		}
	}
	if parsed.mode == "" {
		return parsed, usageError("plan requires --mode proxy-only or tun")
	}
	if parsed.mode != planner.ModeProxyOnly && parsed.mode != planner.ModeTun {
		return parsed, usageError("unsupported plan mode %q", parsed.mode)
	}
	if parsed.profileID == "" {
		return parsed, usageError("plan requires a profile id")
	}
	return parsed, nil
}

func printPlanHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  podlaz plan --mode proxy-only <profile-id> [--json]
  podlaz plan --mode tun <profile-id> [--json] [--verbose|-v] [--plain]

Print a read-only connection plan. TUN planning defaults to a compact human summary. Use --verbose for the full TUN/route/DNS/nftables kill-switch dry-run plan with server bypass, route-loop risk, warnings, and rollback steps. Use --plain for ASCII status markers in human output.
`)
}
