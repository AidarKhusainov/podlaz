package e2e

import (
	"os"
	"strings"
	"testing"
)

func TestHostedVMTCGFourVCPUTrialPreservesKVMTopology(t *testing.T) {
	content, err := os.ReadFile("lib/hosted_vm.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)
	for _, fragment := range []string{
		"local cpu vcpus=2",
		"if [[ \"${HOSTED_VM_ACCEL}\" == kvm ]]; then",
		"cpu=host",
		"cpu=max",
		"vcpus=4",
		"-smp \"${vcpus}\"",
		"-machine \"q35,accel=${HOSTED_VM_ACCEL}\"",
	} {
		if !strings.Contains(script, fragment) {
			t.Fatalf("hosted VM must retain topology and explicit TCG trial: missing %q", fragment)
		}
	}
}
