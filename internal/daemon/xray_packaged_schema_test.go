package daemon

import (
	"context"
	"errors"
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
	assertXrayTunConfigSchemaAccepted(t, xrayPath, minimalPath)

	generated, err := engine.GenerateXrayTunConfig(packagedXrayTunProfileForTest(), engine.XrayTunConfigOptions{Name: engine.DefaultXrayTunName, MTU: engine.DefaultXrayTunMTU, OutboundAddressOverride: "203.0.113.10"})
	if err != nil {
		t.Fatalf("generate representative TUN runtime config: %v", err)
	}
	generatedPath := filepath.Join(dir, "generated-runtime.json")
	if err := os.WriteFile(generatedPath, generated, 0o600); err != nil {
		t.Fatalf("write generated runtime config: %v", err)
	}
	assertXrayTunConfigSchemaAccepted(t, xrayPath, generatedPath)
}

// TestPackagedNMOwnedXraySecurityBoundary validates the official newer runtime
// without silently dropping the supported legacy plaintext-VLESS profile class.
// Production NM admission stays blocked until that compatibility is resolved.
func TestPackagedNMOwnedXraySecurityBoundary(t *testing.T) {
	xrayPath := strings.TrimSpace(os.Getenv("PODLAZ_PACKAGED_XRAY_PATH"))
	if xrayPath == "" {
		t.Skip("PODLAZ_PACKAGED_XRAY_PATH is not set")
	}
	dir := t.TempDir()
	opts := engine.XrayTunConfigOptions{
		Name:                    engine.DefaultXrayTunName,
		MTU:                     engine.DefaultXrayTunMTU,
		OutboundAddressOverride: "203.0.113.10",
	}

	protected := packagedXrayTunProfileForTest()
	protected.Security = "tls"
	protected.ServerName = "vpn.example"
	validConfig, err := engine.GenerateXrayTunConfig(protected, opts)
	if err != nil {
		t.Fatalf("generate protected TUN config: %v", err)
	}
	validPath := filepath.Join(dir, "protected-tun.json")
	if err := os.WriteFile(validPath, validConfig, 0o600); err != nil {
		t.Fatalf("write protected config: %v", err)
	}
	assertXrayTunConfigSchemaAccepted(t, xrayPath, validPath)
	if err := preflightNMOwnedXrayCompatibilityWithRuntime(context.Background(), xrayPath, validConfig); err != nil {
		t.Fatalf("official NM runtime rejected compatible protected profile: %v", err)
	}

	// Previously supported VLESS without TLS remains valid for the default
	// packaged engine. The separately pinned newer engine rejects it; do not
	// erase the production compatibility gap by rewriting the provider config.
	plaintext, err := engine.GenerateXrayTunConfig(packagedXrayTunProfileForTest(), opts)
	if err != nil {
		t.Fatalf("generate plaintext TUN config: %v", err)
	}
	plaintextPath := filepath.Join(dir, "plaintext-tun.json")
	if err := os.WriteFile(plaintextPath, plaintext, 0o600); err != nil {
		t.Fatalf("write plaintext config: %v", err)
	}
	output, err := exec.CommandContext(context.Background(), xrayPath, "run", "-test", "-config", plaintextPath).CombinedOutput()
	if err == nil || !strings.Contains(string(output), "vless without TLS or other encryption is prohibited") {
		t.Fatalf("newer pinned Xray must expose the plaintext VLESS compatibility blocker: exit=%v, output=%s", err, output)
	}
	if err := preflightNMOwnedXrayCompatibilityWithRuntime(context.Background(), xrayPath, plaintext); !errors.Is(err, errNMOwnedXrayIncompatible) {
		t.Fatalf("NM runtime must fail closed for public plaintext VLESS: %v", err)
	}
}

func assertXrayTunConfigSchemaAccepted(t *testing.T, xrayPath, configPath string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), xrayPath, "run", "-test", "-config", configPath)
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err == nil {
		return
	}
	if isXrayTunRuntimeStartupFailure(text) {
		return
	}
	t.Fatalf("%s run -test -config %s rejected config before TUN runtime startup: %v\n%s", xrayPath, configPath, err, text)
}

func isXrayTunRuntimeStartupFailure(text string) bool {
	return strings.Contains(text, "failed to create server") && (strings.Contains(text, "operation not permitted") ||
		strings.Contains(text, "permission denied"))
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
