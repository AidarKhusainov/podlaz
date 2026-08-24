package api

import "fmt"

// TerminalReason is a stable, non-secret product-level classification for a
// conclusively terminated VPN lifecycle. It intentionally does not encode
// low-level probe, transaction, route, DNS, or server details.
type TerminalReason string

const (
	TerminalReasonVPNConnectFailed   TerminalReason = "vpn_connect_failed"
	TerminalReasonVPNRestoreFailed   TerminalReason = "vpn_restore_failed"
	TerminalReasonBootNetworkNotReady TerminalReason = "boot_network_not_ready"
)

func ValidateTerminalReason(reason TerminalReason) error {
	switch reason {
	case "", TerminalReasonVPNConnectFailed, TerminalReasonVPNRestoreFailed, TerminalReasonBootNetworkNotReady:
		return nil
	default:
		return fmt.Errorf("invalid terminal_reason %q", reason)
	}
}
