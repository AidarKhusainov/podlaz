package executor

import (
	"context"
	"strings"
	"testing"
)

func TestTunAddressApplyFinalFenceFailureReturnsNoOwnership(t *testing.T) {
	runner := &recordingRunner{results: []CommandResult{
		{Stdout: tunAddressIdentityLineForTest(7)},
		{Stdout: ""},
		{Stdout: tunAddressIdentityLineForTest(7)},
		{Stdout: tunAddressIdentityLineForTest(8)},
	}}
	exec := IPTunAddressExecutor{Runner: runner}
	step, err := exec.Apply(context.Background(), rollbackIdentityAddressPlanForTest())
	if err == nil {
		t.Fatal("expected final identity fence failure")
	}
	if step.Kind != "" || step.Target != "" || step.Owner != "" {
		t.Fatalf("pre-mutation identity failure returned ownership step: %#v", step)
	}
	for _, command := range runner.commands {
		joined := strings.Join(command, " ")
		if strings.Contains(joined, "address replace") {
			t.Fatalf("address mutation ran after failed final fence: commands=%#v", runner.commands)
		}
	}
}

func tunAddressIdentityLineForTest(ifindex int) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(
		"IFINDEX: podlaz0: <POINTOPOINT,NOARP,UP,LOWER_UP> mtu 1500 qdisc fq_codel state UNKNOWN mode DEFAULT group default qlen 500 tun type tun pi off vnet_hdr on persist off",
		"IFINDEX", string(rune('0'+ifindex))),
		"10", "10"))
}
