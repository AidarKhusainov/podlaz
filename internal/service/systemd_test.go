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
		"ExecStart=/usr/bin/podlazd",
		"User=podlaz",
		"Group=podlaz",
		"UMask=0077",
		"Environment=PODLAZ_SERVICE=systemd",
		"RuntimeDirectory=podlaz",
		"RuntimeDirectoryMode=0710",
		"StateDirectory=podlaz",
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

func TestSystemdUnitDoesNotBlockFutureTunDeviceWork(t *testing.T) {
	content := readSystemdUnit(t)

	for _, forbidden := range []string{
		"Private" + "Devices=yes",
		"Protect" + "KernelTunables=yes",
		"Restrict" + "AddressFamilies=",
	} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("systemd unit contains %q, which would need explicit validation before future TUN/nftables work:\n%s", forbidden, content)
		}
	}
}

func readSystemdUnit(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "packaging", "systemd", "podlazd.service")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read systemd unit: %v", err)
	}
	return string(data)
}
