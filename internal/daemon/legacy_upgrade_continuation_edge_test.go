package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestDecodeLegacyStreamSettingsAcceptsOmittedOptionalTransportObject(t *testing.T) {
	for _, network := range []string{"websocket", "grpc", "httpupgrade"} {
		t.Run(network, func(t *testing.T) {
			settings := map[string]json.RawMessage{
				"network":  json.RawMessage(`"` + network + `"`),
				"security": json.RawMessage(`"none"`),
			}
			var snapshot api.ProfileSnapshot
			if err := decodeLegacyStreamSettings(settings, &snapshot); err != nil {
				t.Fatalf("decode sparse %s stream settings: %v", network, err)
			}
			if snapshot.Transport != network || snapshot.Security != "none" {
				t.Fatalf("decoded sparse stream settings = %#v", snapshot)
			}
		})
	}
}

func TestReadPrivateLegacyRuntimeConfigRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(target, []byte(`{"outbounds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "runtime.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if _, err := readPrivateLegacyRuntimeConfig(link); err == nil {
		t.Fatal("legacy runtime config migration must reject symlink paths")
	}
}
