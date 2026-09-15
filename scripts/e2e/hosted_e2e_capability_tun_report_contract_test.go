package e2e_test

import "testing"

func TestHostedE2ECapabilityClassifiesAuthoritativeTunReportCausePrivately(t *testing.T) {
	workflow := readHostedCapabilityFile(t, hostedCapabilityWorkflow)
	requireHostedCapabilityMarkers(t, workflow,
		"profile-tun-report-cause.log",
		"/run/podlaz/diagnostics/tun-last.json",
		"tun.report.primary_resolved_link_query_failure=observed",
		"tun.report.cause_no_servers=observed",
		"tun.report.cause_timeout=observed",
		"tun.report.cause_link_device=observed",
		"tun.report.cause_other=observed",
	)
	forbidHostedCapabilityMarkers(t, workflow,
		"cat /run/podlaz/diagnostics/tun-last.json",
		"jq . /run/podlaz/diagnostics/tun-last.json",
	)
}
