package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPreflightXrayTunSupportUsesRunTestConfigAndRemovesTrackedRuntimeConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test")
	}
	dir := t.TempDir()
	xray := writeXrayPreflightExecutable(t, filepath.Join(dir, "xray"), `#!/bin/sh
test "$1" = "run"
test "$2" = "-test"
test "$3" = "-config"
test -f "$4"
case "$4" in
  */xray.json) exit 0 ;;
  *) exit 9 ;;
esac
`)
	runtimeConfigPath := filepath.Join(dir, "generated", generatedXrayName)

	err := preflightXrayTunSupport(context.Background(), xray, runtimeConfigPath, []byte("{}\n"), sameUserCoreExecutionIdentity())
	if err != nil {
		t.Fatalf("preflight Xray TUN support: %v", err)
	}
	if _, statErr := os.Stat(runtimeConfigPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected tracked runtime config to be removed after successful preflight, stat err=%v", statErr)
	}
}

func TestPreflightXrayNativeTunSupportUsesMinimalPinnedSchemaConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test")
	}
	dir := t.TempDir()
	observedConfig := filepath.Join(dir, "observed.json")
	t.Setenv("OBSERVED_CONFIG", observedConfig)
	xray := writeXrayPreflightExecutable(t, filepath.Join(dir, "xray"), `#!/bin/sh
cp "$4" "$OBSERVED_CONFIG"
exit 0
`)

	err := preflightXrayNativeTunSupport(context.Background(), xray, sameUserCoreExecutionIdentity())
	if err != nil {
		t.Fatalf("preflight native Xray TUN support: %v", err)
	}
	data, err := os.ReadFile(observedConfig)
	if err != nil {
		t.Fatalf("read observed preflight config: %v", err)
	}
	config := string(data)
	for _, want := range []string{`"protocol": "tun"`, `"name": "podlaz-pf0"`, `"MTU": 1500`, `"userLevel": 0`} {
		if !strings.Contains(config, want) {
			t.Fatalf("minimal preflight config missing %s: %s", want, config)
		}
	}
	for _, forbidden := range []string{`"server"`, `"gateway"`, `"dns"`, `"mtu"`, "podlaz-preflight"} {
		if strings.Contains(config, forbidden) {
			t.Fatalf("minimal preflight config must not contain %s: %s", forbidden, config)
		}
	}
}

func TestMinimalXrayTunPreflightInterfaceNameFitsLinuxIFNAMSIZ(t *testing.T) {
	var cfg struct {
		Inbounds []struct {
			Settings struct {
				Name string `json:"name"`
			} `json:"settings"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(minimalXrayTunPreflightConfig(), &cfg); err != nil {
		t.Fatalf("decode minimal preflight config: %v", err)
	}
	if len(cfg.Inbounds) != 1 {
		t.Fatalf("expected one preflight inbound, got %#v", cfg.Inbounds)
	}
	name := cfg.Inbounds[0].Settings.Name
	if name == "" {
		t.Fatal("expected preflight interface name")
	}
	if len(name) > 15 {
		t.Fatalf("preflight interface name %q is too long for Linux IFNAMSIZ user-visible names", name)
	}
}

func TestPreflightXrayTunSupportReturnsStableUnsupportedError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test")
	}
	dir := t.TempDir()
	xray := writeXrayPreflightExecutable(t, filepath.Join(dir, "xray"), `#!/bin/sh
echo 'unknown inbound protocol: tun' >&2
exit 23
`)
	runtimeConfigPath := filepath.Join(dir, "generated", generatedXrayName)

	err := preflightXrayTunSupport(context.Background(), xray, runtimeConfigPath, []byte("{}\n"), sameUserCoreExecutionIdentity())
	if !errors.Is(err, errXrayTunUnsupported) {
		t.Fatalf("expected Xray TUN unsupported error, got %v", err)
	}
	if !strings.Contains(err.Error(), "TUN mode requires an Xray-core build with tun inbound support") {
		t.Fatalf("expected stable preflight message, got %v", err)
	}
	if strings.Contains(err.Error(), string([]byte{0})) {
		t.Fatalf("unexpected binary data in error: %v", err)
	}
}

func writeXrayPreflightExecutable(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	return path
}
