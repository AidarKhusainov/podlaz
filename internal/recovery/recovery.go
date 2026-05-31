package recovery

import (
	"fmt"
	"strings"
)

// Step describes one recovery action.
type Step struct {
	Name        string
	Description string
	Command     string
}

// PlanResult is the dry-run representation of emergency recovery.
type PlanResult struct {
	Steps []Step
}

// Plan returns the current emergency recovery plan.
//
// It intentionally returns commands as text. Actual execution must be explicit,
// audited, and implemented behind the privileged daemon.
func Plan() PlanResult {
	return PlanResult{Steps: []Step{
		{
			Name:        "stop-daemon",
			Description: "stop the privileged daemon if it is running",
			Command:     "systemctl stop tunwardend.service",
		},
		{
			Name:        "delete-tun",
			Description: "delete the managed TUN interface if it exists",
			Command:     "ip link delete tunwarden0",
		},
		{
			Name:        "flush-rules",
			Description: "remove TunWarden-owned policy routing rules",
			Command:     "ip rule show | grep tunwarden",
		},
		{
			Name:        "flush-nftables",
			Description: "remove TunWarden-owned nftables table",
			Command:     "nft delete table inet tunwarden",
		},
		{
			Name:        "restore-dns",
			Description: "restore DNS from the last committed snapshot",
			Command:     "resolvectl revert tunwarden0",
		},
	}}
}

// String renders the recovery plan in a stable, CLI-friendly format.
func (p PlanResult) String() string {
	var b strings.Builder
	b.WriteString("TunWarden recover plan (dry-run)\n")
	for i, step := range p.Steps {
		fmt.Fprintf(&b, "%d. %s: %s\n   command: %s\n", i+1, step.Name, step.Description, step.Command)
	}
	return b.String()
}
