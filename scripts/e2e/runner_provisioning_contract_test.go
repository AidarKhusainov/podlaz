package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestReleaseTunRunnerHasReproducibleProvisioningContract(t *testing.T) {
	data, err := os.ReadFile("../ops/bootstrap-e2e-runner.sh")
	if err != nil {
		t.Fatalf("read release runner bootstrap: %v", err)
	}
	text := string(data)

	for _, required := range []string{
		`VERSION_ID:-}" == "24.04"`,
		`uname -m`,
		`x86_64`,
		`/dev/net/tun`,
		`systemctl enable --now systemd-resolved.service`,
		`RUNNER_LABELS="self-hosted,linux,x64,vpn-e2e,ubuntu-24.04"`,
		`./config.sh`,
		`--labels "${RUNNER_LABELS}"`,
		`./svc.sh install "${RUNNER_USER}"`,
		`./svc.sh start`,
		`visudo -cf`,
		`/usr/bin/apt`,
		`/usr/bin/systemctl`,
		`/usr/bin/journalctl`,
		`/usr/sbin/ip`,
		`/usr/sbin/nft`,
		`/usr/bin/resolvectl`,
		`/usr/bin/python3`,
		`/usr/bin/rm`,
		`/usr/bin/sha256sum`,
		`/usr/bin/readlink`,
		`/usr/bin/pgrep`,
		`/usr/bin/cat`,
		`/usr/bin/kill`,
		`/usr/bin/deb-systemd-helper`,
		`ALL=(${RUNNER_USER}:podlaz) NOPASSWD: /usr/bin/env, /usr/bin/curl`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("release runner provisioning contract lost %q", required)
		}
	}

	for _, forbidden := range []string{
		"usermod -aG podlaz",
		"suspend-resume",
		"network-reconnect",
		"dhcp-renew",
		"dns-change",
		"polkit-gui-auth",
		"polkit-tty-auth",
		"ubuntu-22.04",
		"debian-12",
		"debian-13",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("release runner bootstrap still carries retired capability %q", forbidden)
		}
	}
}

func TestReleaseTunRunnerLabelsMatchWorkflow(t *testing.T) {
	workflowData, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	bootstrapData, err := os.ReadFile("../ops/bootstrap-e2e-runner.sh")
	if err != nil {
		t.Fatalf("read release runner bootstrap: %v", err)
	}
	labels := "self-hosted,linux,x64,vpn-e2e,ubuntu-24.04"
	workflowLabels := "runs-on: [self-hosted, linux, x64, vpn-e2e, ubuntu-24.04]"
	if !strings.Contains(string(workflowData), workflowLabels) {
		t.Fatalf("release workflow lost dedicated runner labels %q", workflowLabels)
	}
	if !strings.Contains(string(bootstrapData), `RUNNER_LABELS="`+labels+`"`) {
		t.Fatalf("runner bootstrap labels do not match release workflow: %s", labels)
	}
}
