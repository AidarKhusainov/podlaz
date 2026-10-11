package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestHostedTimeBasedQualificationOnlyTracksExternalRisk(t *testing.T) {
	// Expensive deterministic product scenarios must be triggered by source
	// changes or explicit diagnostics, never simply by elapsed time.
	for _, name := range []string{
		"hosted-recovery.yml",
		"hosted-synthetic-tun.yml",
		"hosted-package-history.yml",
		"hosted-package-restart.yml",
		"hosted-apt-repository.yml",
	} {
		t.Run(name, func(t *testing.T) {
			workflow := readScheduledQualificationWorkflow(t, name)
			requireScheduledWorkflowMarkers(t, workflow, "pull_request:", "workflow_dispatch:")
			forbidUnchangedSourceSchedule(t, workflow)
		})
	}

	for _, name := range []string{"hosted-resource-soak.yml", "integration.yml"} {
		t.Run(name, func(t *testing.T) {
			workflow := readScheduledQualificationWorkflow(t, name)
			requireScheduledWorkflowMarkers(t, workflow, "workflow_dispatch:")
			forbidUnchangedSourceSchedule(t, workflow)
		})
	}
}

func TestRecurringSecurityAndDeploymentMonitoringRemainIndependent(t *testing.T) {
	weekly := readScheduledQualificationWorkflow(t, "weekly-dependency-security.yml")
	requireScheduledWorkflowMarkers(t, weekly,
		"schedule:", "govulncheck", "bash scripts/ci/install-go-tool.sh govulncheck")
	if strings.Contains(weekly, "scripts/build-deb.sh") || strings.Contains(weekly, "scripts/e2e/") {
		t.Fatal("weekly dependency security must not build packages or run hosted product E2E")
	}

	availability := readScheduledQualificationWorkflow(t, "public-apt-availability.yml")
	requireScheduledWorkflowMarkers(t, availability,
		"schedule:", "workflow_dispatch:", "bash scripts/ci/public-apt-smoke.sh")
}

func readScheduledQualificationWorkflow(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile("../../.github/workflows/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func requireScheduledWorkflowMarkers(t *testing.T, workflow string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if !strings.Contains(workflow, marker) {
			t.Fatalf("workflow lost mandatory %q", marker)
		}
	}
}

func forbidUnchangedSourceSchedule(t *testing.T, workflow string) {
	t.Helper()
	triggers, _, _ := strings.Cut(workflow, "\njobs:")
	if strings.Contains(triggers, "\n  schedule:") {
		t.Fatal("expensive source-deterministic qualification unexpectedly runs on a timer")
	}
}
