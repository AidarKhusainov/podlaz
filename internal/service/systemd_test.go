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
		"Environment=PODLAZ_XRAY_PATH=/usr/lib/podlaz/xray",
		"RuntimeDirectory=podlaz",
		"RuntimeDirectoryMode=0711",
		"StateDirectory=podlaz",
		"StateDirectoryMode=0700",
		"CapabilityBoundingSet=CAP_CHOWN CAP_SETUID CAP_SETGID CAP_KILL CAP_NET_ADMIN",
		"AmbientCapabilities=CAP_SETUID CAP_KILL CAP_NET_ADMIN",
		"StandardOutput=journal",
		"StandardError=journal",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected systemd unit to contain %q, got:\n%s", want, content)
		}
	}
}

func TestSystemdUnitPreservesExactAuthorityAndOrdersSupervisedShutdown(t *testing.T) {
	content := readSystemdUnit(t)

	for _, want := range []string{
		"KillSignal=SIGTERM",
		"RestartKillSignal=SIGUSR1",
		"KillMode=mixed",
		"RuntimeDirectoryPreserve=yes",
		"TimeoutStopSec=40s",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("restart-safe service contract must contain %q:\n%s", want, content)
		}
	}
	for _, forbidden := range []string{
		"KillSignal=SIGKILL",
		"RestartKillSignal=SIGKILL",
		"KillMode=control-group",
		"KillMode=process",
		"KillMode=none",
		"RuntimeDirectoryPreserve=no",
	} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("restart-safe service contract must not contain %q:\n%s", forbidden, content)
		}
	}
}

func TestSystemdUnitRequiresPolkitAuthorizationForPackagedAccess(t *testing.T) {
	content := readSystemdUnit(t)

	if !strings.Contains(content, "Environment=PODLAZ_POLKIT_AUTHORIZATION=required") {
		t.Fatalf("packaged systemd unit must enable polkit authorization by default:\n%s", content)
	}
	for _, forbidden := range []string{
		"PODLAZ_POLKIT_AUTHORIZATION=disabled",
		"PODLAZ_POLKIT_AUTHORIZATION=false",
		"PODLAZ_POLKIT_AUTHORIZATION=0",
	} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("packaged systemd unit must not disable polkit authorization with %q:\n%s", forbidden, content)
		}
	}
}

func TestSystemdUnitOnlyKeepsRequiredAmbientCapabilities(t *testing.T) {
	content := readSystemdUnit(t)

	if !strings.Contains(content, "AmbientCapabilities=CAP_SETUID CAP_KILL CAP_NET_ADMIN") {
		t.Fatalf("systemd unit must keep CAP_SETUID/CAP_KILL for child lifecycle and CAP_NET_ADMIN for native Xray TUN:\n%s", content)
	}
	for _, forbidden := range []string{
		"AmbientCapabilities=CAP_CHOWN",
		"AmbientCapabilities=CAP_SETGID",
		"AmbientCapabilities=CAP_SYS_ADMIN",
	} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("systemd unit must not grant broad ambient capabilities via %q:\n%s", forbidden, content)
		}
	}
}

func TestSystemdUnitDoesNotBlockTunDeviceWork(t *testing.T) {
	content := readSystemdUnit(t)

	obsoleteHelperName := "tun" + "2socks"
	for _, forbidden := range []string{
		"Private" + "Devices=yes",
		"Protect" + "KernelTunables=yes",
		"Restrict" + "AddressFamilies=",
		"PODLAZ_" + "TUN2SOCKS" + "_PATH",
		"/usr/lib/podlaz/" + obsoleteHelperName,
	} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("systemd unit contains %q, which would need explicit validation before TUN/nftables work:\n%s", forbidden, content)
		}
	}
}

func TestSystemdUnitKeepsBootAutostartInsideDaemonLifecycle(t *testing.T) {
	content := readSystemdUnit(t)

	for _, want := range []string{
		"ExecStart=/usr/bin/podlazd",
		"Restart=on-failure",
		"StateDirectory=podlaz",
		"StateDirectoryMode=0700",
		"RuntimeDirectoryPreserve=yes",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("boot autostart service contract must contain %q:\n%s", want, content)
		}
	}
	for _, forbidden := range []string{
		"ExecStartPre=/usr/bin/podlaz",
		"ExecStartPost=/usr/bin/podlaz",
		"podlaz autostart enable",
		"PODLAZ_AUTOSTART_PROFILE",
		"PODLAZ_AUTOSTART_MODE",
	} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("boot autostart must not use a separate systemd launcher or profile environment via %q:\n%s", forbidden, content)
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
