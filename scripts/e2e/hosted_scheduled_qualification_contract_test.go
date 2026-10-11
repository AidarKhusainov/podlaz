package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestRecurringHostedQualificationRetainsFullMatrix(t *testing.T) {
	// Source changes and runner drift are independent failure domains.
	// Keep low-frequency full regressions and explicitly requested diagnostics.
	for _, item := range []struct{ name, cron string }{
		{"hosted-recovery.yml", "47 4 * * 4"},
		{"hosted-synthetic-tun.yml", "29 4 * * 3"},
		{"hosted-package-history.yml", "41 4 * * 4"},
		{"hosted-package-restart.yml", "13 5 * * 5"},
		{"hosted-apt-repository.yml", "23 5 * * 3"},
		{"hosted-resource-soak.yml", "37 3 * * 0"},
		{"integration.yml", "47 4 * * 0"},
	} {
		t.Run(item.name, func(t *testing.T) {
			workflow := readScheduledQualificationWorkflow(t, item.name)
			triggers, _, _ := strings.Cut(workflow, "\njobs:")
			cron := "- cron: '" + item.cron + "'"
			if strings.Count(triggers, cron) != 1 || strings.Count(triggers, "- cron:") != 1 {
				t.Fatalf("%s must retain precisely one weekly regression schedule", item.name)
			}
			requireScheduledWorkflowMarkers(t, triggers, "workflow_dispatch:")
			if item.name != "hosted-resource-soak.yml" && item.name != "integration.yml" {
				requireScheduledWorkflowMarkers(t, triggers, "pull_request:")
			}
			if item.name == "integration.yml" {
				requireScheduledWorkflowMarkers(t, triggers, "push:", "- master")
			}
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
