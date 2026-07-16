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

func TestStandardProbeGraphSkipsNameDependentLayersAfterResolutionFailure(t *testing.T) {
	probes := StandardProbes(ProbeAdapters{})
	dependencies := make(map[string][]string, len(probes))
	for _, probe := range probes {
		dependencies[probe.Definition.ID] = probe.Definition.DependsOn
	}
	want := map[string]string{
		"tcp-443":                "dns-system-resolution",
		"https-cloudflare-small": "tls",
		"https-google-small":     "dns-system-resolution",
	}
	for id, dependency := range want {
		got := dependencies[id]
		if len(got) != 1 || got[0] != dependency {
			t.Fatalf("probe %s dependencies = %v; want [%s]", id, got, dependency)
		}
	}
}
