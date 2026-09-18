package executor

import (
	"context"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

const tunLinkDetails = `7: podlaz0: <POINTOPOINT,NOARP,UP,LOWER_UP> mtu 1500 qdisc fq_codel state UNKNOWN mode DEFAULT group default qlen 500
    link/none  promiscuity 0 allmulti 0 minmtu 68 maxmtu 65535
    tun type tun pi off vnet_hdr off persist off user 1000 group 1000
`

func TestIPTunDeviceVerifyRequiresTunTypeMTUAndUp(t *testing.T) {
	runner := &recordingRunner{stdout: tunLinkDetails}
	exec := IPTunDeviceExecutor{Runner: runner}

	err := exec.Verify(context.Background(), planner.TunDevicePlan{Name: "podlaz0", MTU: 1500})
	if err != nil {
		t.Fatalf("verify TUN device: %v", err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("expected one command, got %#v", runner.commands)
	}
	got := strings.Join(runner.commands[0], " ")
	if got != "ip -details link show dev podlaz0" {
		t.Fatalf("unexpected verify command: %s", got)
	}
}

func TestIPTunDeviceVerifyRejectsNonTunDevice(t *testing.T) {
	runner := &recordingRunner{stdout: `7: podlaz0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc fq_codel state UP mode DEFAULT group default qlen 1000
    link/ether 02:00:00:00:00:01 brd ff:ff:ff:ff:ff:ff
`}
	exec := IPTunDeviceExecutor{Runner: runner}

	err := exec.Verify(context.Background(), planner.TunDevicePlan{Name: "podlaz0", MTU: 1500})
	if err == nil || !strings.Contains(err.Error(), "not a TUN device") {
		t.Fatalf("expected non-TUN verification failure, got %v", err)
	}
}

func TestIPTunDeviceVerifyRejectsMTUMismatch(t *testing.T) {
	runner := &recordingRunner{stdout: strings.Replace(tunLinkDetails, "mtu 1500", "mtu 1400", 1)}
	exec := IPTunDeviceExecutor{Runner: runner}

	err := exec.Verify(context.Background(), planner.TunDevicePlan{Name: "podlaz0", MTU: 1500})
	if err == nil || !strings.Contains(err.Error(), "MTU does not match") {
		t.Fatalf("expected MTU verification failure, got %v", err)
	}
}

func TestIPTunDeviceVerifyRejectsDownLink(t *testing.T) {
	runner := &recordingRunner{stdout: strings.Replace(tunLinkDetails, "<POINTOPOINT,NOARP,UP,LOWER_UP>", "<POINTOPOINT,NOARP>", 1)}
	exec := IPTunDeviceExecutor{Runner: runner}

	err := exec.Verify(context.Background(), planner.TunDevicePlan{Name: "podlaz0", MTU: 1500})
	if err == nil || !strings.Contains(err.Error(), "not up") {
		t.Fatalf("expected link-up verification failure, got %v", err)
	}
}
