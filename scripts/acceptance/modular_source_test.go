package acceptance_test

import (
	"os/exec"
	"testing"
)

func TestModularReleaseLaptopSource(t *testing.T) {
	cmd := exec.Command("bash", "tests/modular_source_contract.sh")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("modular release-laptop source contract failed: %v\n%s", err, out)
	}
}

func TestPortableReleaseLaptopBundle(t *testing.T) {
	cmd := exec.Command("bash", "tests/portable_bundle_contract.sh")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("portable release-laptop bundle contract failed: %v\n%s", err, out)
	}
}
