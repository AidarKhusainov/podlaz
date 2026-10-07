package profile

import (
	"strings"
	"testing"
)

func TestNativeXraySourceSurvivesProfileStoreRoundTrip(t *testing.T) {
	raw := []byte(`{
	  "remarks":"opaque-roundtrip",
	  "futureTop":{"version":2},
	  "outbounds":[{"protocol":"future-protocol","tag":"future","settings":{"futureOutbound":true},"streamSettings":{"futureStream":"kept"}}],
	  "routing":{"rules":[],"futureRouting":{"mode":"next"}},
	  "balancers":[{"tag":"future-balancer","selector":["future"],"futureBalancer":3}]
	}`)
	p, _, err := NewImportedFileProviderXrayConfig("opaque-roundtrip", raw)
	if err != nil {
		t.Fatalf("create native Xray profile: %v", err)
	}

	store, err := NewStore(t.TempDir() + "/profiles.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(p); err != nil {
		t.Fatalf("persist native Xray profile: %v", err)
	}
	loaded, err := store.Get(p.ID)
	if err != nil {
		t.Fatalf("load native Xray profile: %v", err)
	}
	if loaded.ID != p.ID || loaded.Source != SourceImportedFile || loaded.Protocol != ProtocolXrayJSON {
		t.Fatalf("unexpected loaded metadata: %#v", loaded)
	}
	stored := ProviderXrayConfigJSON(loaded)
	for _, want := range []string{"futureTop", "futureOutbound", "futureStream", "futureRouting", "futureBalancer"} {
		if !strings.Contains(stored, want) {
			t.Fatalf("persist/load lost %q: %s", want, stored)
		}
	}
}
