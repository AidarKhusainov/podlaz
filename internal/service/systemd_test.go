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
		"User=root",
		"Group=podlaz",
		"UMask=0077",
		"Environment=PODLAZ_SERVICE=systemd",
		"Environment=PODLAZ_POLKIT_AUTHORIZATION=required",
		"RuntimeDirectory=podlaz",
		"RuntimeDirectoryMode=0711",
		"StateDirectory=podlaz",
		"StateDirectoryMode=0700",
		"CapabilityBoundingSet=CAP_CHOWN CAP_SETUID CAP_SETGID CAP_KILL CAP_NET_ADMIN",
		"AmbientCapabilities=CAP_SETUID CAP_KILL",
		"StandardOutput=journal",
		"StandardError=journal",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected systemd unit to contain %q, got:\n%s", want, content)
		}
	}
}

func TestSystemdUnitOnlyKeepsChildLifecycleAmbientCapabilities(t *testing.T) {
	content := readSystemdUnit(t)

	if !strings.Contains(content, "AmbientCapabilities=CAP_SETUID CAP_KILL") {
		t.Fatalf("systemd unit must keep only CAP_SETUID and CAP_KILL ambient for daemon-owned Xray child lifecycle:\n%s", content)
	}
	for _, forbidden := range []string{
		"AmbientCapabilities=CAP_CHOWN",
		"AmbientCapabilities=CAP_SETGID",
		"AmbientCapabilities=CAP_NET_ADMIN",
		"AmbientCapabilities=CAP_SYS_ADMIN",
	} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("systemd unit must not grant broad ambient capabilities via %q:\n%s", forbidden, content)
		}
	}
}

func TestSystemdUnitDoesNotBlockTunDeviceWork(t *testing.T) {
	content := readSystemdUnit(t)

	for _, forbidden := range []string{
		"Private" + "Devices=yes",
		"Protect" + "KernelTunables=yes",
		"Restrict" + "AddressFamilies=",
	} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("systemd unit contains %q, which would need explicit validation before TUN/nftables work:\n%s", forbidden, content)
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
