package daemon

import (
	"errors"
	"fmt"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

type tunFailureDiagnosticSummary struct {
	PrimaryClassification tundiag.Classification
	ReportPath            string
	Persisted             bool
}

func (s tunFailureDiagnosticSummary) Empty() bool {
	return s.PrimaryClassification == "" && strings.TrimSpace(s.ReportPath) == "" && !s.Persisted
}

func (s tunFailureDiagnosticSummary) String() string {
	classification := strings.TrimSpace(string(s.PrimaryClassification))
	if classification == "" {
		classification = "unknown"
	}
	location := strings.TrimSpace(s.ReportPath)
	if !s.Persisted || location == "" {
		location = "unavailable"
	}
	return fmt.Sprintf("TUN diagnostics: %s; last report: %s; inspect with: plz doctor --tun --verbose", classification, location)
}

type tunFailureDiagnosticError struct {
	cause   error
	summary tunFailureDiagnosticSummary
}

func (e tunFailureDiagnosticError) Error() string {
	if e.cause == nil {
		return e.summary.String()
	}
	return fmt.Sprintf("%v; %s", e.cause, e.summary.String())
}

func (e tunFailureDiagnosticError) Unwrap() error { return e.cause }

func withTunFailureDiagnosticSummary(err error, summary tunFailureDiagnosticSummary) error {
	if summary.Empty() {
		return err
	}
	return tunFailureDiagnosticError{cause: err, summary: summary}
}

func tunFailureDiagnosticLogFields(err error) (string, string) {
	var diagnosticErr tunFailureDiagnosticError
	if !errors.As(err, &diagnosticErr) {
		return "", ""
	}
	return string(diagnosticErr.summary.PrimaryClassification), diagnosticErr.summary.ReportPath
}
