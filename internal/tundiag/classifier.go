package tundiag

import (
	"fmt"
	"sort"
)

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
	ClassHTTPSPartialFailure,
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
	var providerAdvisory map[string]struct{}
	report, providerAdvisory = applyDerivedClassifications(report)
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
		_, providerFailureIsAdvisory := providerAdvisory[probe.ID]
		pmtuEvidenceOnly := probe.Layer == LayerPMTU && probe.Classification != ClassLikelyPMTUBlackhole
		if probe.Classification != "" && !pmtuEvidenceOnly && !providerFailureIsAdvisory {
			present[probe.Classification] = struct{}{}
		}
		switch probe.Status {
		case ProbeFail:
			if providerFailureIsAdvisory || pmtuEvidenceOnly || isAdvisoryClassification(probe.Classification) {
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
	case ClassHTTPSPartialFailure, ClassDoHPartialFailure, ClassIPv6NotPresent:
		return true
	default:
		return false
	}
}

func applyDerivedClassifications(report Report) (Report, map[string]struct{}) {
	providerAdvisory := make(map[string]struct{})
	report = applyHTTPSProviderQuorum(report, providerAdvisory)
	report = applyDoHProviderQuorum(report, providerAdvisory)

	smallHTTPSPass := false
	pmtuTransportFailures := 0
	for _, probe := range report.Probes {
		if probe.Layer == LayerHTTPS && probe.Status == ProbePass {
			smallHTTPSPass = true
		}
		if probe.Layer == LayerPMTU && probe.Status == ProbeFail && isPMTUTransportFailure(probe) {
			pmtuTransportFailures++
		}
	}
	if smallHTTPSPass && pmtuTransportFailures >= 2 && !containsProbe(report.Probes, "pmtu-corroboration") {
		report.Probes = append(report.Probes, ProbeResult{
			ID:             "pmtu-corroboration",
			Layer:          LayerPMTU,
			Status:         ProbeFail,
			Classification: ClassLikelyPMTUBlackhole,
			Error:          "small HTTPS succeeded while two independent bounded transfers stalled after valid HTTP responses",
		})
	}
	return report, providerAdvisory
}

func applyHTTPSProviderQuorum(report Report, advisory map[string]struct{}) Report {
	cloudflarePass := probeHasStatus(report.Probes, "https-cloudflare-small", ProbePass)
	googlePass := probeHasStatus(report.Probes, "https-google-small", ProbePass)

	if cloudflarePass {
		if failure, found := firstExpectedProviderFailure(report.Probes, "https-google-small"); found {
			advisory[failure.ID] = struct{}{}
			return appendProviderAggregate(report, "https-provider-quorum", LayerHTTPS, ClassHTTPSPartialFailure, failure)
		}
	}
	if googlePass {
		if failure, found := firstExpectedProviderFailure(report.Probes, "tcp-443", "tls", "https-cloudflare-small"); found {
			advisory[failure.ID] = struct{}{}
			return appendProviderAggregate(report, "https-provider-quorum", LayerHTTPS, ClassHTTPSPartialFailure, failure)
		}
	}
	return report
}

func applyDoHProviderQuorum(report Report, advisory map[string]struct{}) Report {
	cloudflarePass := probeHasStatus(report.Probes, "doh-cloudflare", ProbePass)
	googlePass := probeHasStatus(report.Probes, "doh-google", ProbePass)

	if cloudflarePass {
		if failure, found := firstExpectedProviderFailure(report.Probes, "doh-google"); found {
			advisory[failure.ID] = struct{}{}
			return appendProviderAggregate(report, "doh-provider-quorum", LayerDoH, ClassDoHPartialFailure, failure)
		}
	}
	if googlePass {
		if failure, found := firstExpectedProviderFailure(report.Probes, "doh-cloudflare"); found {
			advisory[failure.ID] = struct{}{}
			return appendProviderAggregate(report, "doh-provider-quorum", LayerDoH, ClassDoHPartialFailure, failure)
		}
	}
	return report
}

func firstExpectedProviderFailure(probes []ProbeResult, ids ...string) (ProbeResult, bool) {
	for _, id := range ids {
		probe, ok := probeByID(probes, id)
		if !ok || probe.Status != ProbeFail {
			continue
		}
		if expectedProviderFailure(probe) {
			return probe, true
		}
		return ProbeResult{}, false
	}
	return ProbeResult{}, false
}

func expectedProviderFailure(probe ProbeResult) bool {
	if probe.Classification == ClassTimeout {
		return true
	}
	switch probe.ID {
	case "tcp-443":
		return probe.Classification == ClassTCP443Failure
	case "tls":
		return probe.Classification == ClassTLSFailure
	case "https-cloudflare-small", "https-google-small":
		return probe.Classification == ClassHTTPSFailure
	case "doh-cloudflare", "doh-google":
		return probe.Classification == ClassDoHFailure
	default:
		return false
	}
}

func appendProviderAggregate(report Report, id string, layer Layer, classification Classification, failure ProbeResult) Report {
	if containsProbe(report.Probes, id) {
		return report
	}
	report.Probes = append(report.Probes, ProbeResult{
		ID:             id,
		Layer:          layer,
		Status:         ProbeFail,
		Classification: classification,
		Error:          fmt.Sprintf("provider %s failed while an independent provider succeeded", failure.ID),
		Evidence:       Evidence{Notes: []string{"root classification preserved on probe " + failure.ID}},
	})
	return report
}

func probeHasStatus(probes []ProbeResult, id string, status ProbeStatus) bool {
	probe, ok := probeByID(probes, id)
	return ok && probe.Status == status
}

func probeByID(probes []ProbeResult, id string) (ProbeResult, bool) {
	for _, probe := range probes {
		if probe.ID == id {
			return probe, true
		}
	}
	return ProbeResult{}, false
}

func isPMTUTransportFailure(probe ProbeResult) bool {
	if probe.Evidence.HTTP == nil || !probe.Evidence.HTTP.ResponseAccepted {
		return false
	}
	switch probe.Evidence.HTTP.FailurePhase {
	case "body_timeout", "body_transport":
		return true
	default:
		return false
	}
}

func containsProbe(probes []ProbeResult, id string) bool {
	_, ok := probeByID(probes, id)
	return ok
}
