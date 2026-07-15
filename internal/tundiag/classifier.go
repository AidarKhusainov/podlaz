package tundiag

import "sort"

var primaryClassificationPriority = []Classification{
	ClassSessionInactive,
	ClassSessionMetadataInconsistent,
	ClassOwnershipMismatch,
	ClassServerBypassFailure,
	ClassPolicyRuleFailure,
	ClassRouteFailure,
	ClassForeignDNSConflict,
	ClassDNSApplyFailure,
	ClassDNSUDPFailure,
	ClassDNSTCPFailure,
	ClassDNSResolutionFailure,
	ClassDNSHijackDetected,
	ClassTCP443Failure,
	ClassTLSFailure,
	ClassHTTPSFailure,
	ClassDoHFailure,
	ClassIPv6Leak,
	ClassIPv6Unusable,
	ClassLikelyPMTUBlackhole,
	ClassTimeout,
	ClassCancelled,
	ClassInternalDiagnosticError,
	ClassDoHPartialFailure,
	ClassIPv6NotPresent,
}

var pmtuSuppressors = map[Classification]struct{}{
	ClassServerBypassFailure:  {},
	ClassRouteFailure:         {},
	ClassPolicyRuleFailure:    {},
	ClassDNSApplyFailure:      {},
	ClassForeignDNSConflict:   {},
	ClassDNSUDPFailure:        {},
	ClassDNSTCPFailure:        {},
	ClassDNSResolutionFailure: {},
	ClassDNSHijackDetected:    {},
	ClassTLSFailure:           {},
}

func Finalize(report Report) Report {
	report = applyDerivedClassifications(report)
	if report.SchemaVersion == 0 {
		report.SchemaVersion = SchemaVersion
	}
	if report.Probes == nil {
		report.Probes = []ProbeResult{}
	}
	if report.Warnings == nil {
		report.Warnings = []string{}
	}
	if report.Errors == nil {
		report.Errors = []string{}
	}

	present := make(map[Classification]struct{})
	unhealthy := false
	degraded := false
	for _, probe := range report.Probes {
		if probe.Classification != "" {
			present[probe.Classification] = struct{}{}
		}
		switch probe.Status {
		case ProbeFail:
			if isAdvisoryClassification(probe.Classification) {
				degraded = true
			} else {
				unhealthy = true
			}
		case ProbeSkipped:
			if probe.Classification == ClassCancelled || probe.Classification == ClassTimeout {
				unhealthy = true
			}
		}
	}
	if len(report.Errors) > 0 {
		unhealthy = true
	}
	if len(report.Warnings) > 0 {
		degraded = true
	}

	if _, hasPMTU := present[ClassLikelyPMTUBlackhole]; hasPMTU && hasAnyClassification(present, pmtuSuppressors) {
		delete(present, ClassLikelyPMTUBlackhole)
	}
	report.PrimaryClassification = choosePrimary(present)

	switch {
	case report.PrimaryClassification == ClassSessionInactive:
		report.Status = StatusUnavailable
	case unhealthy:
		report.Status = StatusUnhealthy
	case degraded:
		report.Status = StatusDegraded
	default:
		report.Status = StatusHealthy
	}
	return SanitizeReport(report)
}

func choosePrimary(present map[Classification]struct{}) Classification {
	for _, classification := range primaryClassificationPriority {
		if _, ok := present[classification]; ok {
			return classification
		}
	}
	if len(present) == 0 {
		return ""
	}
	values := make([]string, 0, len(present))
	for classification := range present {
		values = append(values, string(classification))
	}
	sort.Strings(values)
	return Classification(values[0])
}

func hasAnyClassification(present map[Classification]struct{}, candidates map[Classification]struct{}) bool {
	for classification := range candidates {
		if _, ok := present[classification]; ok {
			return true
		}
	}
	return false
}

func isAdvisoryClassification(classification Classification) bool {
	switch classification {
	case ClassDoHPartialFailure, ClassIPv6NotPresent:
		return true
	default:
		return false
	}
}

func applyDerivedClassifications(report Report) Report {
	dohPasses := 0
	dohFailures := 0
	for _, probe := range report.Probes {
		if probe.Layer != LayerDoH {
			continue
		}
		switch probe.Status {
		case ProbePass:
			dohPasses++
		case ProbeFail:
			dohFailures++
		}
	}
	if dohPasses > 0 && dohFailures > 0 {
		for i := range report.Probes {
			if report.Probes[i].Layer == LayerDoH && report.Probes[i].Status == ProbeFail {
				report.Probes[i].Classification = ClassDoHPartialFailure
			}
		}
	}

	smallHTTPSPass := false
	pmtuTimeoutFailures := 0
	for _, probe := range report.Probes {
		if probe.Layer == LayerHTTPS && probe.Status == ProbePass {
			smallHTTPSPass = true
		}
		if probe.Layer == LayerPMTU && probe.Status == ProbeFail && (probe.Classification == ClassTimeout || probe.Classification == ClassHTTPSFailure) {
			pmtuTimeoutFailures++
		}
	}
	if smallHTTPSPass && pmtuTimeoutFailures >= 2 && !containsProbe(report.Probes, "pmtu-corroboration") {
		report.Probes = append(report.Probes, ProbeResult{
			ID:             "pmtu-corroboration",
			Layer:          LayerPMTU,
			Status:         ProbeFail,
			Classification: ClassLikelyPMTUBlackhole,
			Error:          "small HTTPS succeeded while two independent bounded 16 KiB transfers failed",
		})
	}
	return report
}

func containsProbe(probes []ProbeResult, id string) bool {
	for _, probe := range probes {
		if probe.ID == id {
			return true
		}
	}
	return false
}
