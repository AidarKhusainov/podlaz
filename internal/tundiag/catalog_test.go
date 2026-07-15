package tundiag

import "testing"

func TestCatalogHasIndependentDoHProvidersAndBoundedPMTUTargets(t *testing.T) {
	doh := TargetsByKind(TargetDoH)
	if len(doh) < 2 || doh[0].Host == doh[1].Host {
		t.Fatalf("expected independent DoH providers, got %#v", doh)
	}
	for _, target := range append(doh, TargetsByKind(TargetPMTU)...) {
		if target.Timeout <= 0 || target.MaxResponseBytes <= 0 || target.PrivacyNote == "" {
			t.Fatalf("target is not bounded/documented: %#v", target)
		}
	}
}
