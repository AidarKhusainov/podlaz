package profile

import "testing"

func TestDeduplicateDisplayNamesAvoidsFinalNameCollisions(t *testing.T) {
	profiles := []Profile{
		{Name: "Name", Protocol: "vless", Server: "one.example", Port: 443},
		{Name: "Name", Protocol: "vless", Server: "two.example", Port: 443},
		{Name: "Name (2)", Protocol: "vless", Server: "three.example", Port: 443},
	}

	DeduplicateDisplayNames(profiles)

	assertDisplayNames(t, profiles, "Name", "Name (2)", "Name (2) (2)")
}

func TestDeduplicateDisplayNamesKeepsSuffixSearchDeterministic(t *testing.T) {
	profiles := []Profile{
		{Name: "Name", Protocol: "vless", Server: "one.example", Port: 443},
		{Name: "Name (2)", Protocol: "vless", Server: "two.example", Port: 443},
		{Name: "Name", Protocol: "vless", Server: "three.example", Port: 443},
	}

	DeduplicateDisplayNames(profiles)

	assertDisplayNames(t, profiles, "Name", "Name (2)", "Name (3)")
}

func assertDisplayNames(t *testing.T, profiles []Profile, names ...string) {
	t.Helper()
	if len(profiles) != len(names) {
		t.Fatalf("expected %d profiles, got %#v", len(names), profiles)
	}
	for i, name := range names {
		if profiles[i].Name != name {
			t.Fatalf("expected profile %d name %q, got %#v", i, name, profiles)
		}
	}
}
