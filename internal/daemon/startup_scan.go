package daemon

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/doctor"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
	"github.com/AidarKhusainov/podlaz/internal/render"
)

type startupScanFunc func(context.Context) recovery.PlanResult

type startupScanState struct {
	refreshMu         sync.Mutex
	refreshDone       chan struct{}
	refreshGeneration uint64
	mu                sync.RWMutex
	scan              recovery.PlanResult
	scanFn            startupScanFunc
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
	if ctx == nil {
		ctx = context.Background()
	}

	for {
		s.refreshMu.Lock()
		generation := s.refreshGeneration
		if done := s.refreshDone; done != nil {
			s.refreshMu.Unlock()
			select {
			case <-done:
				s.refreshMu.Lock()
				current := generation == s.refreshGeneration
				s.refreshMu.Unlock()
				if current {
					return s.Snapshot()
				}
				continue
			case <-ctx.Done():
				return incompleteStartupScan("wait for concurrent recovery scan: " + ctx.Err().Error())
			}
		}
		return s.startRefreshLocked(ctx, generation)
	}
}

// ForceRefresh guarantees the scan it returns starts after the caller's mutation
// boundary. It invalidates any older running generation before waiting so the
// older scan cannot publish stale pre-mutation candidates after the mutation.
func (s *startupScanState) ForceRefresh(ctx context.Context) recovery.PlanResult {
	if s == nil || s.scanFn == nil {
		return recovery.PlanResult{}
	}
	if ctx == nil {
		ctx = context.Background()
	}

	s.refreshMu.Lock()
	s.refreshGeneration++
	generation := s.refreshGeneration
	for {
		if done := s.refreshDone; done != nil {
			s.refreshMu.Unlock()
			select {
			case <-done:
				s.refreshMu.Lock()
				continue
			case <-ctx.Done():
				scan := incompleteStartupScan("wait for concurrent recovery scan: " + ctx.Err().Error())
				s.publishScan(generation, scan)
				return scan
			}
		}
		return s.startRefreshLocked(ctx, generation)
	}
}

func (s *startupScanState) startRefreshLocked(ctx context.Context, generation uint64) recovery.PlanResult {
	done := make(chan struct{})
	s.refreshDone = done
	s.refreshMu.Unlock()
	defer s.finishRefresh(done)

	scan := cloneRecoveryPlan(s.scanFn(ctx))
	if err := ctx.Err(); err != nil {
		scan = incompleteStartupScan("authoritative refresh did not complete: " + err.Error())
	}
	if !s.publishScan(generation, scan) {
		return incompleteStartupScan("authoritative refresh was superseded by a newer generation")
	}
	return scan
}

func (s *startupScanState) publishScan(generation uint64, scan recovery.PlanResult) bool {
	cloned := cloneRecoveryPlan(scan)
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	if generation != s.refreshGeneration {
		return false
	}
	s.mu.Lock()
	s.scan = cloned
	s.mu.Unlock()
	return true
}

func incompleteStartupScan(message string) recovery.PlanResult {
	return recovery.PlanResult{Warnings: []recovery.Warning{{Target: "recovery scan", Message: message}}}
}

func (s *startupScanState) finishRefresh(done chan struct{}) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	if s.refreshDone != done {
		return
	}
	close(done)
	s.refreshDone = nil
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

func withStartupScanDoctor(response api.DoctorResponse, scan recovery.PlanResult, status api.StatusResponse) api.DoctorResponse {
	check := api.DoctorCheck{
		Name:     "startup-recovery-scan",
		Severity: string(doctor.SeverityOK),
		Message:  startupScanDoctorMessage(scan, status),
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
		log.Printf("podlazd: startup recovery scan: %s", render.Redact(status))
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
	log.Printf("podlazd: %s", render.Redact(strings.Join(parts, "; ")))
}

func startupScanDoctorMessage(scan recovery.PlanResult, status api.StatusResponse) string {
	humanStatus := startupScanHumanStatus(startupScanStatus(scan))
	if startupScanStatus(scan) == api.StartupScanStatusClean && status.Connection == "active" {
		humanStatus = "clean for active connection"
	}
	parts := []string{"startup recovery scan: " + humanStatus}
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
		return "podlaz recover"
	}
	if len(scan.Warnings) > 0 {
		return "podlaz doctor"
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
