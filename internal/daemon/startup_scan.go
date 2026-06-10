package daemon

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/AidarKhusainov/tunwarden/internal/api"
	"github.com/AidarKhusainov/tunwarden/internal/doctor"
	"github.com/AidarKhusainov/tunwarden/internal/recovery"
	"github.com/AidarKhusainov/tunwarden/internal/render"
)

type startupScanFunc func(context.Context) recovery.PlanResult

type startupScanState struct {
	mu     sync.RWMutex
	scan   recovery.PlanResult
	scanFn startupScanFunc
}

func defaultStartupScanFunc(runtimeDir string) startupScanFunc {
	return func(ctx context.Context) recovery.PlanResult {
		return recovery.PlanWithOptions(ctx, recovery.Options{RuntimeDir: runtimeDir})
	}
}

func newStartupScanState(scanFn startupScanFunc) *startupScanState {
	return &startupScanState{scanFn: scanFn}
}

func (s *startupScanState) Refresh(ctx context.Context) recovery.PlanResult {
	if s == nil || s.scanFn == nil {
		return recovery.PlanResult{}
	}
	scan := cloneRecoveryPlan(s.scanFn(ctx))
	s.mu.Lock()
	s.scan = cloneRecoveryPlan(scan)
	s.mu.Unlock()
	return scan
}

func (s *startupScanState) Snapshot() recovery.PlanResult {
	if s == nil {
		return recovery.PlanResult{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneRecoveryPlan(s.scan)
}

func withStartupScanStatus(status api.StatusResponse, scan recovery.PlanResult) api.StatusResponse {
	status.StartupScan = startupScanToAPI(scan)
	return status
}

func withStartupScanDoctor(response api.DoctorResponse, scan recovery.PlanResult) api.DoctorResponse {
	check := api.DoctorCheck{
		Name:     "startup-recovery-scan",
		Severity: string(doctor.SeverityOK),
		Message:  startupScanDoctorMessage(scan),
	}
	if len(scan.Candidates) > 0 || len(scan.Warnings) > 0 {
		check.Severity = string(doctor.SeverityWarning)
	}
	response.Checks = append(response.Checks, check)
	return response
}

func startupScanToAPI(scan recovery.PlanResult) *api.StartupScanStatus {
	out := api.StartupScanStatus{
		Status:          startupScanStatus(scan),
		Candidates:      make([]api.RecoveryCandidate, 0, len(scan.Candidates)),
		Warnings:        make([]api.RecoveryWarning, 0, len(scan.Warnings)),
		SuggestedAction: startupScanSuggestedAction(scan),
	}
	for _, candidate := range scan.Candidates {
		out.Candidates = append(out.Candidates, recoveryCandidateToAPI(candidate))
	}
	for _, warning := range scan.Warnings {
		out.Warnings = append(out.Warnings, api.RecoveryWarning{Target: warning.Target, Message: warning.Message})
	}
	return &out
}

func logStartupScan(scan recovery.PlanResult) {
	status := startupScanHumanStatus(startupScanStatus(scan))
	if len(scan.Candidates) == 0 && len(scan.Warnings) == 0 {
		log.Printf("tunwardend: startup recovery scan: %s", render.Redact(status))
		return
	}

	parts := []string{fmt.Sprintf("startup recovery scan: %s", status)}
	if txID := firstStartupTransactionID(scan); txID != "" {
		parts = append(parts, "pending transaction: "+txID)
	}
	if len(scan.Candidates) > 0 {
		parts = append(parts, fmt.Sprintf("recovery candidates: %d", len(scan.Candidates)))
	}
	if len(scan.Warnings) > 0 {
		parts = append(parts, fmt.Sprintf("inspection warnings: %d", len(scan.Warnings)))
	}
	if action := startupScanSuggestedAction(scan); action != "" {
		parts = append(parts, "suggested action: "+action)
	}
	log.Printf("tunwardend: %s", render.Redact(strings.Join(parts, "; ")))
}

func startupScanDoctorMessage(scan recovery.PlanResult) string {
	parts := []string{"startup recovery scan: " + startupScanHumanStatus(startupScanStatus(scan))}
	if txID := firstStartupTransactionID(scan); txID != "" {
		parts = append(parts, "pending transaction: "+txID)
	}
	if len(scan.Candidates) > 0 {
		parts = append(parts, fmt.Sprintf("recovery candidates: %d", len(scan.Candidates)))
	}
	if len(scan.Warnings) > 0 {
		parts = append(parts, fmt.Sprintf("inspection warnings: %d", len(scan.Warnings)))
	}
	if action := startupScanSuggestedAction(scan); action != "" {
		parts = append(parts, "suggested action: "+action)
	}
	return render.Redact(strings.Join(parts, "; "))
}

func startupScanStatus(scan recovery.PlanResult) string {
	switch {
	case len(scan.Candidates) > 0 && len(scan.Warnings) > 0:
		return api.StartupScanStatusStaleIncomplete
	case len(scan.Candidates) > 0:
		return api.StartupScanStatusStale
	case len(scan.Warnings) > 0:
		return api.StartupScanStatusIncomplete
	default:
		return api.StartupScanStatusClean
	}
}

func startupScanHumanStatus(status string) string {
	switch status {
	case api.StartupScanStatusStale:
		return "stale state found"
	case api.StartupScanStatusIncomplete:
		return "inspection incomplete"
	case api.StartupScanStatusStaleIncomplete:
		return "stale state found (inspection incomplete)"
	default:
		return "clean inactive state"
	}
}

func startupScanSuggestedAction(scan recovery.PlanResult) string {
	if len(scan.Candidates) > 0 {
		return "tunwarden recover"
	}
	if len(scan.Warnings) > 0 {
		return "tunwarden doctor"
	}
	return ""
}

func firstStartupTransactionID(scan recovery.PlanResult) string {
	for _, candidate := range scan.Candidates {
		if candidate.Transaction != nil && strings.TrimSpace(candidate.Transaction.ID) != "" {
			return candidate.Transaction.ID
		}
	}
	return ""
}

func cloneRecoveryPlan(in recovery.PlanResult) recovery.PlanResult {
	out := recovery.PlanResult{
		Candidates: make([]recovery.Candidate, 0, len(in.Candidates)),
		Warnings:   append([]recovery.Warning(nil), in.Warnings...),
	}
	for _, candidate := range in.Candidates {
		cloned := candidate
		if candidate.Transaction != nil {
			tx := *candidate.Transaction
			cloned.Transaction = &tx
		}
		out.Candidates = append(out.Candidates, cloned)
	}
	return out
}
