//go:build linux

package daemon

import (
	"strings"
	"testing"
)

func TestNMRestrictedXrayChildCannotBeCalledAsOrdinaryDaemonFunction(t *testing.T) {
	err := RunNMOwnedRestrictedXrayChild("/run/podlaz/generated/xray.json")
	if err == nil {
		t.Fatal("restricted child accepted invocation without the exact launcher arguments")
	}
	if !strings.Contains(err.Error(), "invocation") &&
		!strings.Contains(err.Error(), "privileged broker") {
		t.Fatalf("unexpected restricted child refusal: %v", err)
	}
}

func TestNMRestrictedXrayChildDoesNotExposeNetworkConfiguration(t *testing.T) {
	err := RunNMOwnedRestrictedXrayChild("example.invalid/private-profile")
	if err == nil {
		t.Fatal("invalid restricted child invocation was accepted")
	}
	if strings.Contains(err.Error(), "private-profile") {
		t.Fatalf("restricted child error disclosed a generated config location: %v", err)
	}
}
