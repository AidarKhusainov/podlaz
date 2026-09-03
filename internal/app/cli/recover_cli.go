package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/client"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
	"github.com/AidarKhusainov/podlaz/internal/render"
)

type recoverArgs struct {
	execute bool
	yes     bool
	json    bool
}

type recoverPlanView struct {
	recovery.PlanResult
	NetworkSession *api.NetworkSessionRecoveryState
}

type recoverExecuteView struct {
	recovery.ExecuteResult
	NetworkSession *api.NetworkSessionRecoveryState
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
	if result.HasIncompleteCleanup() {
		return exitError{code: 1, err: errors.New("recover completed with incomplete cleanup")}
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
	return confirmDefaultYes(stdout, confirmationReader(opts), "Recover will ask podlazd to remove only clearly podlaz-owned stale state. Type yes to continue", "recovery", "recover canceled")
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

func runRecover(ctx context.Context, opts options) recoverPlanView {
	if opts.recover != nil {
		return recoverPlanView{PlanResult: opts.recover(ctx)}
	}
	startup, ok := daemonStartupRecoverPlan(ctx)
	if ok {
		return startup
	}
	return recoverPlanView{PlanResult: recovery.Plan(ctx)}
}

func daemonStartupRecoverPlan(ctx context.Context) (recoverPlanView, bool) {
	socketPath := api.SocketPath("")
	if _, err := os.Stat(socketPath); err != nil {
		return recoverPlanView{}, false
	}
	response, err := (client.StatusClient{SocketPath: socketPath}).Status(ctx)
	if err != nil || response.StartupScan == nil {
		return recoverPlanView{}, false
	}
	return recoverPlanView{
		PlanResult:     recoveryPlanFromStartupScan(*response.StartupScan),
		NetworkSession: api.CloneNetworkSessionRecoveryState(response.StartupScan.NetworkSession),
	}, true
}

func recoveryPlanFromStartupScan(scan api.StartupScanStatus) recovery.PlanResult {
	candidates := make([]recovery.Candidate, 0, len(scan.Candidates))
	for _, candidate := range scan.Candidates {
		candidates = append(candidates, recoveryCandidateFromAPI(candidate))
	}
	warnings := make([]recovery.Warning, 0, len(scan.Warnings))
	for _, warning := range scan.Warnings {
		warnings = append(warnings, recovery.Warning{Target: warning.Target, Message: warning.Message})
	}
	return recovery.PlanResult{Candidates: candidates, Warnings: warnings}
}

func runRecoverExecute(ctx context.Context, opts options) (recoverExecuteView, error) {
	if opts.recoverExecute != nil {
		result, err := opts.recoverExecute(ctx)
		return recoverExecuteView{ExecuteResult: result}, err
	}
	response, err := (client.RecoveryClient{}).Recover(ctx)
	if err != nil {
		return recoverExecuteView{}, err
	}
	return recoverExecuteView{
		ExecuteResult:  recoveryResultFromAPI(response),
		NetworkSession: api.CloneNetworkSessionRecoveryState(response.NetworkSession),
	}, nil
}

func (p recoverPlanView) String() string {
	base := p.PlanResult.String()
	if p.NetworkSession == nil {
		return base
	}
	base = strings.Replace(base, "No podlaz-owned recovery candidates found.\n", "", 1)
	return strings.Replace(base, "No changes were applied.\n", networkSessionRecoveryHuman(p.NetworkSession)+"No changes were applied.\n", 1)
}

func (r recoverExecuteView) String() string {
	base := r.ExecuteResult.String()
	if r.NetworkSession == nil {
		return base
	}
	base = strings.Replace(base, "No podlaz-owned recovery candidates found.\n", "", 1)
	return strings.Replace(base, "No non-podlaz resources were touched.\n", networkSessionRecoveryHuman(r.NetworkSession)+"No non-podlaz resources were touched.\n", 1)
}

func (r recoverExecuteView) HasIncompleteCleanup() bool {
	if r.ExecuteResult.HasIncompleteCleanup() {
		return true
	}
	if r.NetworkSession == nil {
		return false
	}
	return r.NetworkSession.StartupGate == api.NetworkSessionStartupGateBlocked ||
		r.NetworkSession.NextAction != api.NetworkSessionRecoveryActionNone
}

func networkSessionRecoveryHuman(state *api.NetworkSessionRecoveryState) string {
	if state == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Network Session authority: %s\n", render.Redact(state.Authority))
	fmt.Fprintf(&b, "Intent: %s\n", render.Redact(state.Intent))
	fmt.Fprintf(&b, "Startup gate: %s\n", render.Redact(state.StartupGate))
	if state.ResumeStage != "" {
		fmt.Fprintf(&b, "Resume stage: %s\n", render.Redact(state.ResumeStage))
	}
	fmt.Fprintf(&b, "Last resume outcome: %s\n", render.Redact(state.LastResumeOutcome))
	if state.LastTUNFailurePhase != "" {
		fmt.Fprintf(&b, "TUN failure phase: %s\n", render.Redact(state.LastTUNFailurePhase))
	}
	if state.RollbackStatus != "" {
		fmt.Fprintf(&b, "Rollback status: %s\n", render.Redact(state.RollbackStatus))
	}
	fmt.Fprintf(&b, "Transaction present: %t\n", state.TransactionPresent)
	fmt.Fprintf(&b, "Legacy migration: %t\n", state.LegacyMigration)
	fmt.Fprintf(&b, "Cleanup authority: %s\n", render.Redact(state.CleanupAuthority))
	fmt.Fprintf(&b, "Next action: %s\n", render.Redact(state.NextAction))
	return b.String()
}

func recoverPlanJSON(plan recoverPlanView) map[string]any {
	recoveryPayload := redactedRecoveryPlan(plan.PlanResult)
	if plan.NetworkSession != nil {
		recoveryPayload["network_session"] = redactedNetworkSessionRecoveryState(plan.NetworkSession)
	}
	payload := okJSON(map[string]any{
		"mode":     "dry-run",
		"recovery": recoveryPayload,
	})
	var warnings []string
	if len(plan.Candidates) > 0 {
		warnings = append(warnings, "recovery candidates require cleanup")
	}
	if plan.NetworkSession != nil {
		warnings = append(warnings, "network session recovery authority requires convergence")
	}
	if len(plan.Warnings) > 0 {
		warnings = append(warnings, "recovery inspection is incomplete")
	}
	if len(warnings) > 0 {
		payload["status"] = "warn"
		payload["warnings"] = warnings
	}
	return payload
}

func recoverExecuteJSON(result recoverExecuteView) map[string]any {
	status := "ok"
	errorsOut := []string{}
	if result.HasFailures() {
		status = "fail"
		errorsOut = append(errorsOut, "recover completed with cleanup failures")
	} else if result.HasIncompleteCleanup() {
		status = "warn"
		errorsOut = append(errorsOut, "recover completed with incomplete cleanup")
	}
	payload := map[string]any{
		"schema_version": "v1",
		"status":         status,
		"warnings":       redactedRecoveryWarnings(result.Warnings),
		"errors":         errorsOut,
		"mode":           "execute",
		"recovery":       redactedCleanupResults(result.Results),
	}
	if result.NetworkSession != nil {
		payload["network_session"] = redactedNetworkSessionRecoveryState(result.NetworkSession)
	}
	return payload
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

func redactedNetworkSessionRecoveryState(state *api.NetworkSessionRecoveryState) map[string]any {
	if state == nil {
		return nil
	}
	return map[string]any{
		"authority":              render.Redact(state.Authority),
		"intent":                 render.Redact(state.Intent),
		"startup_gate":           render.Redact(state.StartupGate),
		"resume_stage":           render.Redact(state.ResumeStage),
		"last_resume_outcome":    render.Redact(state.LastResumeOutcome),
		"last_tun_failure_phase": render.Redact(state.LastTUNFailurePhase),
		"rollback_status":        render.Redact(state.RollbackStatus),
		"transaction_present":    state.TransactionPresent,
		"legacy_migration":       state.LegacyMigration,
		"cleanup_authority":      render.Redact(state.CleanupAuthority),
		"next_action":            render.Redact(state.NextAction),
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
