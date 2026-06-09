package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/AidarKhusainov/tunwarden/internal/api"
	"github.com/AidarKhusainov/tunwarden/internal/client"
	"github.com/AidarKhusainov/tunwarden/internal/recovery"
	"github.com/AidarKhusainov/tunwarden/internal/render"
)

type recoverArgs struct {
	execute bool
	yes     bool
	json    bool
}

func runRecoverCommand(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	if isHelp(args) {
		printRecoverHelp(stdout)
		return nil
	}
	parsed, err := parseRecoverArgs(args)
	if err != nil {
		return err
	}
	if !parsed.execute {
		plan := runRecover(ctx, opts)
		if parsed.json {
			return writeJSON(stdout, recoverPlanJSON(plan))
		}
		fmt.Fprint(stdout, plan.String())
		return nil
	}
	if !parsed.yes {
		if parsed.json {
			return usageError("recover --execute --json requires --yes")
		}
		if err := confirmRecoverExecute(stdout, opts); err != nil {
			return err
		}
	}

	result, err := runRecoverExecute(ctx, opts)
	if err != nil {
		return lifecycleCommandError(err)
	}
	if parsed.json {
		if err := writeJSON(stdout, recoverExecuteJSON(result)); err != nil {
			return err
		}
	} else {
		fmt.Fprint(stdout, result.String())
	}
	if result.HasFailures() {
		return exitError{code: 1, err: errors.New("recover completed with cleanup failures")}
	}
	return nil
}

func parseRecoverArgs(args []string) (recoverArgs, error) {
	var parsed recoverArgs
	for _, arg := range args {
		switch arg {
		case "--execute":
			parsed.execute = true
		case "--yes":
			parsed.yes = true
		case "--json":
			parsed.json = true
		default:
			return parsed, usageError("unsupported recover argument %q", arg)
		}
	}
	if parsed.yes && !parsed.execute {
		return parsed, usageError("recover --yes requires --execute")
	}
	return parsed, nil
}

func confirmRecoverExecute(stdout io.Writer, opts options) error {
	if !recoverInputIsTerminal(opts) {
		return usageError("recover --execute requires --yes in non-interactive mode")
	}
	reader := opts.stdin
	if reader == nil {
		reader = os.Stdin
	}
	fmt.Fprint(stdout, "Recover will ask tunwardend to remove only clearly TunWarden-owned stale state. Type yes to continue: ")
	line, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read recovery confirmation: %w", err)
	}
	if strings.EqualFold(strings.TrimSpace(line), "yes") {
		return nil
	}
	return exitError{code: 1, err: errors.New("recover canceled")}
}

func recoverInputIsTerminal(opts options) bool {
	if opts.stdinIsTerminal != nil {
		return opts.stdinIsTerminal()
	}
	return isStdinTerminal()
}

func isStdinTerminal() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func runRecover(ctx context.Context, opts options) recovery.PlanResult {
	if opts.recover != nil {
		return opts.recover(ctx)
	}
	return recovery.Plan(ctx)
}

func runRecoverExecute(ctx context.Context, opts options) (recovery.ExecuteResult, error) {
	if opts.recoverExecute != nil {
		return opts.recoverExecute(ctx)
	}
	response, err := (client.RecoveryClient{}).Recover(ctx)
	if err != nil {
		return recovery.ExecuteResult{}, err
	}
	return recoveryResultFromAPI(response), nil
}

func recoverPlanJSON(plan recovery.PlanResult) map[string]any {
	return okJSON(map[string]any{
		"mode":     "dry-run",
		"recovery": redactedRecoveryPlan(plan),
	})
}

func recoverExecuteJSON(result recovery.ExecuteResult) map[string]any {
	status := "ok"
	errorsOut := []string{}
	if result.HasFailures() {
		status = "fail"
		errorsOut = append(errorsOut, "recover completed with cleanup failures")
	}
	return map[string]any{
		"schema_version": "v1",
		"status":         status,
		"warnings":       redactedRecoveryWarnings(result.Warnings),
		"errors":         errorsOut,
		"mode":           "execute",
		"recovery":       redactedCleanupResults(result.Results),
	}
}

func recoveryResultFromAPI(response api.RecoveryResponse) recovery.ExecuteResult {
	results := make([]recovery.CleanupResult, 0, len(response.Results))
	for _, result := range response.Results {
		results = append(results, recovery.CleanupResult{
			Candidate: recoveryCandidateFromAPI(result.Candidate),
			Status:    result.Status,
			Message:   result.Message,
		})
	}
	warnings := make([]recovery.Warning, 0, len(response.Warnings))
	for _, warning := range response.Warnings {
		warnings = append(warnings, recovery.Warning{Target: warning.Target, Message: warning.Message})
	}
	return recovery.ExecuteResult{Results: results, Warnings: warnings}
}

func recoveryCandidateFromAPI(candidate api.RecoveryCandidate) recovery.Candidate {
	out := recovery.Candidate{Kind: candidate.Kind, Description: candidate.Description, Target: candidate.Target}
	if candidate.Transaction != nil {
		out.Transaction = &recovery.TransactionCandidate{
			ID:                candidate.Transaction.ID,
			State:             candidate.Transaction.State,
			Status:            candidate.Transaction.Status,
			RollbackAvailable: candidate.Transaction.RollbackAvailable,
			RequiresCleanup:   candidate.Transaction.RequiresCleanup,
			Path:              candidate.Transaction.Path,
		}
	}
	return out
}

func redactedRecoveryPlan(plan recovery.PlanResult) map[string]any {
	return map[string]any{
		"candidates": redactedRecoveryCandidates(plan.Candidates),
		"warnings":   redactedRecoveryWarnings(plan.Warnings),
	}
}

func redactedCleanupResults(results []recovery.CleanupResult) []map[string]any {
	out := make([]map[string]any, 0, len(results))
	for _, result := range results {
		item := map[string]any{
			"candidate": redactedRecoveryCandidate(result.Candidate),
			"status":    render.Redact(result.Status),
		}
		if strings.TrimSpace(result.Message) != "" {
			item["message"] = render.Redact(result.Message)
		}
		out = append(out, item)
	}
	return out
}

func redactedRecoveryCandidates(candidates []recovery.Candidate) []map[string]any {
	out := make([]map[string]any, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, redactedRecoveryCandidate(candidate))
	}
	return out
}

func redactedRecoveryCandidate(candidate recovery.Candidate) map[string]any {
	out := map[string]any{
		"kind":        render.Redact(candidate.Kind),
		"description": render.Redact(candidate.Description),
		"target":      render.Redact(candidate.Target),
	}
	if candidate.Transaction != nil {
		out["transaction"] = map[string]any{
			"id":                 render.Redact(candidate.Transaction.ID),
			"state":              render.Redact(candidate.Transaction.State),
			"status":             render.Redact(candidate.Transaction.Status),
			"rollback_available": candidate.Transaction.RollbackAvailable,
			"requires_cleanup":   candidate.Transaction.RequiresCleanup,
			"path":               render.Redact(candidate.Transaction.Path),
		}
	}
	return out
}

func redactedRecoveryWarnings(warnings []recovery.Warning) []map[string]string {
	out := make([]map[string]string, 0, len(warnings))
	for _, warning := range warnings {
		out = append(out, map[string]string{
			"target":  render.Redact(warning.Target),
			"message": render.Redact(warning.Message),
		})
	}
	return out
}
