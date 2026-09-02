package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
)

func TestActiveProxyOnlyOwnsItsGeneratedRuntimeConfigWithoutTransaction(t *testing.T) {
	runtimeDir := t.TempDir()
	generatedDir := filepath.Join(runtimeDir, generatedDirName)
	if err := os.MkdirAll(generatedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(generatedDir, generatedXrayName)
	if err := os.WriteFile(configPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	scan := recovery.PlanResult{Candidates: []recovery.Candidate{{
		Kind:        "generated-runtime-configs",
		Description: "generated runtime configs",
		Target:      generatedDir,
	}}}
	status := api.StatusResponse{
		Connection:        "active",
		Mode:              planner.ModeProxyOnly,
		RuntimeConfigPath: configPath,
	}

	for i := 0; i < 2; i++ {
		got := filterStartupScanForActiveRuntime(scan, status, runtimeDir)
		if len(got.Candidates) != 0 || len(got.Warnings) != 0 {
			t.Fatalf("status pass %d: expected clean active proxy-only scan, got candidates=%v warnings=%v", i+1, got.Candidates, got.Warnings)
		}
	}
}

func TestActiveProxyOnlyKeepsForeignGeneratedArtifactVisible(t *testing.T) {
	runtimeDir := t.TempDir()
	generatedDir := filepath.Join(runtimeDir, generatedDirName)
	if err := os.MkdirAll(generatedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(generatedDir, generatedXrayName)
	if err := os.WriteFile(configPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(generatedDir, "foreign.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	scan := recovery.PlanResult{Candidates: []recovery.Candidate{{Kind: "generated-runtime-configs", Target: generatedDir}}}
	status := api.StatusResponse{Connection: "active", Mode: planner.ModeProxyOnly, RuntimeConfigPath: configPath}
	got := filterStartupScanForActiveRuntime(scan, status, runtimeDir)
	if len(got.Candidates) != 1 {
		t.Fatalf("expected foreign generated artifact to remain visible, got %#v", got.Candidates)
	}
	if len(got.Warnings) != 0 {
		t.Fatalf("did not expect synthetic transaction warning for proxy-only, got %#v", got.Warnings)
	}
}
