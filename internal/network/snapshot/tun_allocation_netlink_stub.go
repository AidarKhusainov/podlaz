//go:build !linux

package snapshot

import (
	"context"
	"errors"
)

func CollectTunAllocationEvidence(context.Context) (TunAllocationEvidence, error) {
	return TunAllocationEvidence{}, errors.New("TUN allocation evidence is available only on Linux")
}
