//go:build linux

package daemon

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// The exact dedicated broker, not the default podlazd.service, must have
// these effective capabilities prior to any FD generation reservation.
// CAP_SETPCAP is required to drop all unwanted bounding bits in the child;
// CAP_NET_RAW is needed by the marked Xray transport. The default packaged
// daemon deliberately does not provide them and must remain unchanged.
var nmBrokerRequiredCapabilities = []int{
	unix.CAP_NET_ADMIN,
	unix.CAP_NET_RAW,
	unix.CAP_SETUID,
	unix.CAP_SETGID,
	unix.CAP_SETPCAP,
}

func verifyNMGenerationBrokerCapabilities() error {
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var data [2]unix.CapUserData
	if err := unix.Capget(&header, &data[0]); err != nil {
		return errors.New("NM Xray broker capability evidence unavailable")
	}
	var permitted [2]uint32
	var effective [2]uint32
	for i := range data {
		permitted[i] = data[i].Permitted
		effective[i] = data[i].Effective
	}
	if err := requireNMGenerationCapabilities(permitted, effective); err != nil {
		return err
	}
	for _, capability := range nmBrokerRequiredCapabilities {
		value, err := unix.PrctlRetInt(unix.PR_CAPBSET_READ, uintptr(capability), 0, 0, 0)
		if err != nil || value != 1 {
			return fmt.Errorf("NM Xray broker lacks capability %d in bounding set", capability)
		}
	}
	return nil
}

func requireNMGenerationCapabilities(permitted, effective [2]uint32) error {
	for _, capability := range nmBrokerRequiredCapabilities {
		index := capability / 32
		mask := uint32(1) << uint(capability%32)
		if permitted[index]&mask == 0 || effective[index]&mask == 0 {
			return fmt.Errorf("NM Xray broker lacks active capability %d", capability)
		}
	}
	return nil
}
