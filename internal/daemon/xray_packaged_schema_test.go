package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/engine"
	"github.com/AidarKhusainov/podlaz/internal/profile"
)

func TestPackagedXrayAcceptsPinnedTunConfigs(t *testing.T) {
	xrayPath := strings.TrimSpace(os.Getenv("PODLAZ_PACKAGED_XRAY_PATH"))
	if xrayPath == "" {
		t.Skip("PODLAZ_PACKAGED_XRAY_PATH is not set")
	}
	dir := t.TempDir()

	minimalPath := filepath.Join(dir, "minimal-preflight.json")
	if err := os.WriteFile(minimalPath, minimalXrayTunPreflightConfig(), 0o600); err != nil {
		t.Fatalf("write minimal preflight config: %v", err)
	}
	assertXrayConfigTest(t, xrayPath, minimalPath)

	generated, err := engine.GenerateXrayTunConfig(packagedXrayTunProfileForTest(), engine.XrayTunConfigOptions{Name: engine.DefaultXrayTunName, MTU: engine.DefaultXrayTunMTU, OutboundAddressOverride: "203.0.113.10"})
	if err != nil {
		t.Fatalf("generate representative TUN runtime config: %v", err)
	}
	generatedPath := filepath.Join(dir, "generated-runtime.json")
	if err := os.WriteFile(generatedPath, generated, 0o600); err != nil {
		t.Fatalf("write generated runtime config: %v", err)
	}
	assertXrayConfigTest(t, xrayPath, generatedPath)
}

func assertXrayConfigTest(t *testing.T, xrayPath, configPath string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), xrayPath, "run", "-test", "-config", configPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s run -test -config %s failed: %v\n%s", xrayPath, configPath, err, strings.TrimSpace(string(output)))
	}
}

func packagedXrayTunProfileForTest() profile.Profile {
	return profile.Profile{
		ID:           "packaged-xray-tun-schema",
		Name:         "packaged xray tun schema",
		Source:       profile.SourceImportedFile,
		Engine:       profile.EngineXray,
		Server:       "vpn.example",
		Port:         443,
		Protocol:     "vless",
		UserIdentity: "00000000-0000-4000-8000-000000000701",
		Transport:    "tcp",
		Security:     "none",
		Encryption:   "none",
	}
}
