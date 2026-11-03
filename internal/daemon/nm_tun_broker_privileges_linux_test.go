//go:build linux

package daemon

import (
	"fmt"
	"testing"

	)

func TestNMGenerationBrokerRequiresEveryCapabilityBeforeAdmission(t *testing.T) {
	var full [2]uint32
	for _, capability := range nmBrokerRequiredCapabilities {
		full[capability/32] |= uint32(1) << uint(capability%32)
	}
	if err := requireNMGenerationCapabilities(full, full); err != nil {
		t.Fatalf("complete broker capability set refused: %v", err)
	}
	for _, cap := range nmBrokerRequiredCapabilities {
		t.Run(fmt.Sprintf("cap-%d", cap), func(t *testing.T) {
			mask := uint32(1) << uint(cap%32)
			for _, kind := range []string{"permitted", "effective"} {
				permitted, effective := full, full
				if kind == "permitted" {
					permitted[cap/32] &^= mask
				} else {
					effective[cap/32] &^= mask
				}
				if err := requireNMGenerationCapabilities(permitted, effective); err == nil {
					t.Fatalf("missing %s capability %d admitted", kind, cap)
				}
			}
		})
	}
}
