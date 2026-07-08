package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/render"
)

var errXrayTunUnsupported = errors.New("TUN mode requires an Xray-core build with tun inbound support")

const xrayTunPreflightInterfaceName = "podlaz-pf0"

func preflightXrayNativeTunSupport(ctx context.Context, xrayPath string, identity coreExecutionIdentity) error {
	dir, err := os.MkdirTemp("", "podlaz-xray-tun-preflight-*")
	if err != nil {
		return fmt.Errorf("create Xray TUN preflight temp directory: %w", err)
	}
	defer os.RemoveAll(dir)

	err = preflightXrayTunSupport(ctx, xrayPath, filepath.Join(dir, generatedXrayName), minimalXrayTunPreflightConfig(), identity)
	if errors.Is(err, errXrayTunUnsupported) {
		return wrapRuntimeUnavailable("Xray TUN support", err)
	}
	return err
}

func minimalXrayTunPreflightConfig() []byte {
	return []byte(`{
  "inbounds": [
    {
      "tag": "podlaz-tun-preflight",
      "protocol": "tun",
      "settings": {
        "name": "` + xrayTunPreflightInterfaceName + `",
        "MTU": 1500,
        "userLevel": 0
      }
    }
  ],
  "outbounds": [
    {
      "tag": "direct",
      "protocol": "freedom"
    }
  ]
}
`)
}

func preflightXrayTunSupport(ctx context.Context, xrayPath, runtimeConfigPath string, xrayConfig []byte, identity coreExecutionIdentity) error {
	if strings.TrimSpace(xrayPath) == "" {
		return errors.New("missing Xray binary path for TUN preflight")
	}
	if strings.TrimSpace(runtimeConfigPath) == "" {
		return errors.New("missing Xray runtime config path for TUN preflight")
	}
	if len(xrayConfig) == 0 {
		return errors.New("missing Xray TUN preflight config")
	}
	dir := filepath.Dir(runtimeConfigPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create generated config directory for TUN preflight: %w", err)
	}
	if err := writeRuntimeConfig(runtimeConfigPath, xrayConfig, identity.runtimeConfigPermissions()); err != nil {
		return fmt.Errorf("write Xray TUN preflight config: %w", err)
	}
	defer removeGeneratedConfig(runtimeConfigPath)

	cmd := exec.CommandContext(ctx, xrayPath, "run", "-test", "-config", runtimeConfigPath)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	configureCoreCommandCredential(cmd, identity)
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(render.Redact(output.String()))
		if detail == "" {
			return errXrayTunUnsupported
		}
		return fmt.Errorf("%w: %s", errXrayTunUnsupported, detail)
	}
	return nil
}
