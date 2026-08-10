//go:build linux

package daemon

import (
	"encoding/binary"
	"testing"

	"golang.org/x/sys/unix"
)

func TestTunNetlinkTriggersClassifyLinkAddressAndRouteWithoutUsingPayloadState(t *testing.T) {
	payload := append(issue245NetlinkMessage(unix.RTM_NEWLINK), issue245NetlinkMessage(unix.RTM_NEWADDR)...)
	payload = append(payload, issue245NetlinkMessage(unix.RTM_NEWROUTE)...)
	triggers, err := tunNetlinkTriggers(payload)
	if err != nil {
		t.Fatalf("parse netlink triggers: %v", err)
	}
	want := []tunRevalidationTrigger{tunRevalidationTriggerLink, tunRevalidationTriggerAddress, tunRevalidationTriggerRoute}
	if len(triggers) != len(want) {
		t.Fatalf("triggers=%v, want %v", triggers, want)
	}
	for i := range want {
		if triggers[i] != want[i] {
			t.Fatalf("trigger[%d]=%q, want %q", i, triggers[i], want[i])
		}
	}
}

func TestTunNetlinkTriggersRejectMalformedMessage(t *testing.T) {
	payload := issue245NetlinkMessage(unix.RTM_NEWROUTE)
	binary.NativeEndian.PutUint32(payload[:4], 8)
	if _, err := tunNetlinkTriggers(payload); err == nil {
		t.Fatal("expected malformed netlink message to fail closed")
	}
}

func issue245NetlinkMessage(messageType uint16) []byte {
	const headerLength = 16
	message := make([]byte, headerLength)
	binary.NativeEndian.PutUint32(message[0:4], headerLength)
	binary.NativeEndian.PutUint16(message[4:6], messageType)
	return message
}
