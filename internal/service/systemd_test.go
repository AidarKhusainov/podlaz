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
		"ExecStart=/usr/local/bin/tunwardend",
		"User=root",
		"Group=tunwarden",
		"UMask=0007",
		"Environment=TUNWARDEN_SERVICE=systemd",
		"RuntimeDirectory=tunwarden",
		"RuntimeDirectoryMode=0750",
		"StateDirectory=tunwarden",
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
		"PrivateDevices=yes",
		"ProtectKernelTunables=yes",
		"RestrictAddressFamilies=",
	} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("systemd unit contains %q, which would need explicit validation before future TUN/nftables work:\n%s", forbidden, content)
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
