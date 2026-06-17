package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemdUnitDocumentsSocketAccessModel(t *testing.T) {
	content := readSystemdUnit(t)

	for _, want := range []string{
		"ExecStart=/usr/bin/tunwardend",
		"User=tunwarden",
		"Group=tunwarden",
		"UMask=0077",
		"Environment=TUNWARDEN_SERVICE=systemd",
		"RuntimeDirectory=tunwarden",
		"RuntimeDirectoryMode=0710",
		"StateDirectory=tunwarden",
		"StateDirectoryMode=0700",
		"CapabilityBoundingSet=",
		"AmbientCapabilities=",
		"StandardOutput=journal",
		"StandardError=journal",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected systemd unit to contain %q, got:\n%s", want, content)
		}
	}
}

func readSystemdUnit(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "packaging", "systemd", "tunwardend.service")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read systemd unit: %v", err)
	}
	return string(data)
}
