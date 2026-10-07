package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/profile"
)

func TestNativeXrayProxyOnlyCompositionPreservesUnknownSourceFields(t *testing.T) {
	raw := []byte(`{
	  "futureTop":{"enabled":true},
	  "inbounds":[{"tag":"provider-in","protocol":"dokodemo-door","port":1}],
	  "outbounds":[{"tag":"proxy","protocol":"future-protocol","settings":{"futureOutbound":true},"streamSettings":{"futureStream":{"v":1}}}],
	  "routing":{"rules":[{"outboundTag":"proxy","domain":["example.test"],"futureRouting":"kept"}]},
	  "balancers":[{"tag":"auto","selector":["proxy"],"futureBalancer":"kept"}]
	}`)
	p, _, err := profile.NewImportedFileProviderXrayConfig("native", raw)
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	generated, err := GenerateXrayProxyOnlyConfig(p, DefaultXrayProxyOnlyConfigOptions())
	if err != nil {
		t.Fatalf("generate proxy-only config: %v", err)
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(generated, &doc); err != nil {
		t.Fatalf("decode runtime config: %v", err)
	}
	text := string(generated)
	for _, want := range []string{"futureTop", "futureOutbound", "futureStream", "futureRouting", "futureBalancer"} {
		if !strings.Contains(text, want) {
			t.Fatalf("runtime composition lost %q: %s", want, text)
		}
	}
	if strings.Contains(text, "provider-in") {
		t.Fatalf("provider-owned inbound must not survive Podlaz runtime overlay: %s", text)
	}
}

func TestNativeXrayProxyOnlyRejectsProviderInboundRoutingAuthority(t *testing.T) {
	raw := []byte(`{
	  "outbounds":[{"tag":"proxy","protocol":"freedom"}],
	  "routing":{"rules":[{"inboundTag":["provider-in"],"outboundTag":"proxy"}]}
	}`)
	p, _, err := profile.NewImportedFileProviderXrayConfig("unsafe", raw)
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	err = ValidateXrayProxyOnlyProfile(p)
	if err == nil || !strings.Contains(err.Error(), "inboundTag is not supported") {
		t.Fatalf("expected structural safety rejection, got %v", err)
	}
}
