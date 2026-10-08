package sub

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/engine"
)

// Run with the exact packaged Xray executable after the Debian package build.
// No secrets or raw Xray diagnostics are printed into public CI artifacts.
func TestMihomoHysteria2PackagedXrayAcceptsMarkedNativeConfig(t *testing.T) {
	binary := os.Getenv("PODLAZ_TEST_XRAY_BINARY")
	if binary == "" {
		t.Skip("packaged Xray binary not supplied")
	}
	local, err := ParseLocalImportContent([]byte(mihomoHysteria2Fixture))
	if err != nil {
		t.Fatal(err)
	}
	config, err := engine.GenerateXrayTunConfig(local.Profiles[0], engine.XrayTunConfigOptions{
		Name: "pz-hy2-check", MTU: 1400, EgressMark: 32191,
	})
	if err != nil {
		t.Fatal("failed to compose Hysteria2 native TUN configuration")
	}
	proxyOnly, proxyErr := engine.GenerateProviderXrayProxyOnlyConfig(local.Profiles[0], engine.XrayProxyOnlyConfigOptions{
		SOCKSListen: "127.0.0.1", SOCKSPort: 19871, HTTPListen: "127.0.0.1", HTTPPort: 19872,
	})
	if proxyErr != nil {
		t.Fatal("failed to compose Hysteria2 proxy-only configuration")
	}
	t.Run("proxy-only", func(t *testing.T) {
		checkBundledHysteriaConfig(t, binary, proxyOnly, false)
	})
	t.Run("marked-native-TUN", func(t *testing.T) {
		checkBundledHysteriaConfig(t, binary, config, true)
	})
}

func checkBundledHysteriaConfig(t *testing.T, binary string, config []byte, isolated bool) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "synthetic.json")
	if err := os.WriteFile(path, config, 0600); err != nil {
		t.Fatal("failed to create private synthetic Xray configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Xray initializes its native TUN during -test. Use a disposable network
	// namespace with CAP_NET_ADMIN; never create the device on the host.
	command := exec.CommandContext(ctx, binary, "run", "-test", "-config", path)
	if isolated {
		command = exec.CommandContext(ctx, "sudo", "-n", "unshare", "--net", "--",
			binary, "run", "-test", "-config", path)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		// Never print unredacted Xray diagnostics into CI logs.
		diagnostic := string(output)
		for _, secret := range []string{"synthetic-example-password", "hy2.example.com", "edge.example.com", path} {
			diagnostic = strings.ReplaceAll(diagnostic, secret, "[redacted]")
		}
		t.Fatalf("bundled Xray rejected synthetic Hysteria2 native TUN configuration: %s", diagnostic)
	}
}
