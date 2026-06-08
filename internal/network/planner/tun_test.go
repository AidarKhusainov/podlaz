package planner

import (
	"strings"
	"testing"

	"github.com/AidarKhusainov/tunwarden/internal/network/snapshot"
)

func TestPlanTunBuildsReadOnlyPlanFromFakeSnapshot(t *testing.T) {
	plan, err := PlanTun(testVLESSProfile(), snapshot.FakeResolvedDesktop())
	if err != nil {
		t.Fatalf("plan tun: %v", err)
	}

	if plan.Mode != ModeTun {
		t.Fatalf("expected mode %q, got %q", ModeTun, plan.Mode)
	}
	if plan.Snapshot.DefaultIPv4.Interface != "wlp0s20f3" {
		t.Fatalf("expected fake default interface, got %#v", plan.Snapshot.DefaultIPv4)
	}
	if len(plan.RollbackSteps) != 0 {
		t.Fatalf("read-only snapshot plan should not need rollback steps, got %#v", plan.RollbackSteps)
	}
	for _, step := range plan.Steps {
		lower := strings.ToLower(step)
		if strings.Contains(lower, "apply") || strings.Contains(lower, "create tun") || strings.Contains(lower, "delete") {
			t.Fatalf("TUN snapshot plan contains a mutation step: %q", step)
		}
	}
}

func TestPlanTunWarnsAboutMissingOptionalToolsAndStaleResources(t *testing.T) {
	plan, err := PlanTun(testVLESSProfile(), snapshot.FakeDesktopWithStaleTunWardenResources())
	if err != nil {
		t.Fatalf("plan tun: %v", err)
	}
	if !containsWarning(plan.Warnings, "stale TunWarden-owned") {
		t.Fatalf("expected stale-resource warning, got %#v", plan.Warnings)
	}

	plan, err = PlanTun(testVLESSProfile(), snapshot.FakeDesktopWithoutOptionalTools())
	if err != nil {
		t.Fatalf("plan tun without optional tools: %v", err)
	}
	for _, want := range []string{"systemd-resolved", "nftables"} {
		if !containsWarning(plan.Warnings, want) {
			t.Fatalf("expected warning containing %q, got %#v", want, plan.Warnings)
		}
	}
}

func containsWarning(warnings []string, want string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, want) {
			return true
		}
	}
	return false
}
