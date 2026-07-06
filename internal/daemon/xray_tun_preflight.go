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

func preflightXrayTunSupport(ctx context.Context, xrayPath, runtimeConfigPath string, xrayConfig []byte, identity coreExecutionIdentity) error {
	if strings.TrimSpace(xrayPath) == "" {
		return errors.New("missing Xray binary path for TUN preflight")
	}
	if len(xrayConfig) == 0 {
		return errors.New("missing Xray TUN preflight config")
	}
	dir := filepath.Dir(runtimeConfigPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create generated config directory for TUN preflight: %w", err)
	}
	preflightPath := filepath.Join(dir, "xray-tun-preflight.json")
	if err := writeRuntimeConfig(preflightPath, xrayConfig, identity.runtimeConfigPermissions()); err != nil {
		return fmt.Errorf("write Xray TUN preflight config: %w", err)
	}
	defer removeGeneratedConfig(preflightPath)

	cmd := exec.CommandContext(ctx, xrayPath, "test", "-config", preflightPath)
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
