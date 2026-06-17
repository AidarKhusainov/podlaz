package daemon

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"testing"
)

type testPolkitPolicy struct {
	Actions []testPolkitAction `xml:"action"`
}

type testPolkitAction struct {
	ID       string             `xml:"id,attr"`
	Defaults testPolkitDefaults `xml:"defaults"`
}

type testPolkitDefaults struct {
	AllowAny      string `xml:"allow_any"`
	AllowInactive string `xml:"allow_inactive"`
	AllowActive   string `xml:"allow_active"`
}

func TestPackagedPolkitPolicyDefinesConservativeOperationActions(t *testing.T) {
	policyPath := filepath.Join("..", "..", "packaging", "polkit-1", "actions", "io.github.aidarkhusainov.tunwarden.policy")
	content, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatalf("read polkit policy: %v", err)
	}
	var policy testPolkitPolicy
	if err := xml.Unmarshal(content, &policy); err != nil {
		t.Fatalf("parse polkit policy: %v", err)
	}

	want := map[string]struct{}{
		string(ActionConnectProxyOnly): {},
		string(ActionConnectTun):       {},
		string(ActionDisconnect):       {},
		string(ActionRecoverExecute):   {},
	}
	got := make(map[string]testPolkitDefaults, len(policy.Actions))
	for _, action := range policy.Actions {
		got[action.ID] = action.Defaults
	}
	for action := range want {
		defaults, ok := got[action]
		if !ok {
			t.Fatalf("missing polkit action %s", action)
		}
		for name, value := range map[string]string{
			"allow_any":      defaults.AllowAny,
			"allow_inactive": defaults.AllowInactive,
			"allow_active":   defaults.AllowActive,
		} {
			if value == "" {
				t.Fatalf("action %s has empty %s default", action, name)
			}
			if value == "yes" {
				t.Fatalf("action %s grants broad yes in %s", action, name)
			}
		}
	}
}
