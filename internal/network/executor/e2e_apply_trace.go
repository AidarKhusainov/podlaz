package executor

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	e2eApplyTraceGateEnv = "PODLAZ_E2E_TUN_ROLLBACK_PAUSE"
	e2eApplyTraceDirEnv  = "PODLAZ_E2E_TUN_ROLLBACK_PAUSE_DIR"
	e2eApplyTraceArmFile = "rollback-pause.arm"
)

func recordE2EApplyTrace(event string) {
	switch event {
	case "dnsaware-entered",
		"dnsaware-validate-dns-failed",
		"dnsaware-validate-firewall-failed",
		"dnsaware-validate-base-failed",
		"dnsaware-validate-passed",
		"tun-base-validate-passed",
		"tun-preapply-started",
		"tun-preapply-passed",
		"tun-preapply-failed",
		"tun-address-apply-started",
		"tun-address-apply-passed",
		"tun-address-apply-failed":
	default:
		return
	}
	gate := strings.TrimSpace(os.Getenv(e2eApplyTraceGateEnv))
	if gate != "1" && !strings.EqualFold(gate, "true") {
		return
	}
	dir := strings.TrimSpace(os.Getenv(e2eApplyTraceDirEnv))
	if dir == "" {
		return
	}
	dir = filepath.Clean(dir)
	if _, err := os.Stat(filepath.Join(dir, e2eApplyTraceArmFile)); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "apply-trace."+event), []byte("1\n"), 0o600)
}
