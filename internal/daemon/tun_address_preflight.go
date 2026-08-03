package daemon

import (
	"errors"
	"fmt"
	"strings"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func requireTunAddressPreflight(plan planner.TunPlan) error {
	address := plan.TunAddress
	if strings.TrimSpace(address.CIDR) == "" {
		return errors.New("TUN plan is missing the daemon-owned IPv4 address")
	}
	switch address.Action {
	case planner.TunAddressActionAssign:
		return nil
	case planner.TunAddressActionBlocked:
		return fmt.Errorf("%w: %s", netexecutor.ErrTunAddressConflict, strings.TrimSpace(address.Reason))
	case planner.TunAddressActionDaemonRecheck:
		return fmt.Errorf("%w: authoritative daemon-side IPv4 address and route inspection is incomplete: %s", netexecutor.ErrTunAddressConflict, strings.TrimSpace(address.Reason))
	default:
		return fmt.Errorf("%w: unsupported TUN address action %q", netexecutor.ErrTunAddressConflict, address.Action)
	}
}
