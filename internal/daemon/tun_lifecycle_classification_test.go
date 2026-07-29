package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

func TestTunLifecycleFailureClassificationUsesStableTaxonomy(t *testing.T) {
	tests := []struct {
		name  string
		phase string
		cause error
		want  tundiag.Classification
	}{
		{name: "network apply", phase: "network-apply", cause: errors.New("apply failed"), want: tundiag.ClassNetworkApplyFailure},
		{name: "network verify", phase: "network-verify", cause: errors.New("verify failed"), want: tundiag.ClassNetworkVerifyFailure},
		{name: "cancelled", phase: "network-verify", cause: context.Canceled, want: tundiag.ClassCancelled},
		{name: "timeout", phase: "network-verify", cause: context.DeadlineExceeded, want: tundiag.ClassTimeout},
		{name: "unknown lifecycle", phase: "connectivity-verify", cause: errors.New("unknown"), want: tundiag.ClassInternalDiagnosticError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tunLifecycleFailureClassification(tt.phase, tt.cause); got != tt.want {
				t.Fatalf("unexpected lifecycle classification: got %q want %q", got, tt.want)
			}
			report := tundiag.Finalize(appendTunLifecycleFailureProbe(tundiag.Report{}, tt.phase, tt.cause))
			if report.PrimaryClassification != tt.want {
				t.Fatalf("JSON primary classification drifted: got %q want %q", report.PrimaryClassification, tt.want)
			}
			if report.PrimaryClassification == tundiag.Classification(report.Status) {
				t.Fatalf("overall status leaked into classification taxonomy: %#v", report)
			}
		})
	}
}
