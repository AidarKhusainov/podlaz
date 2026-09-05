package snapshot

import (
	"context"
	"errors"
	"fmt"
)

const tunAllocationDumpAttempts = 3

var errTunAllocationDumpInterrupted = errors.New("TUN allocation netlink dump interrupted")

type tunAllocationDumpFunc func(context.Context) (TunAllocationEvidence, error)

func retryTunAllocationEvidenceDump(ctx context.Context, dump tunAllocationDumpFunc) (TunAllocationEvidence, error) {
	for attempt := 1; attempt <= tunAllocationDumpAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return TunAllocationEvidence{}, err
		}
		evidence, err := dump(ctx)
		if err == nil {
			return evidence, nil
		}
		if !errors.Is(err, errTunAllocationDumpInterrupted) {
			return TunAllocationEvidence{}, err
		}
		if attempt == tunAllocationDumpAttempts {
			return TunAllocationEvidence{}, fmt.Errorf("collect TUN allocation evidence after %d interrupted dumps: %w", attempt, err)
		}
	}
	return TunAllocationEvidence{}, errors.New("unreachable TUN allocation retry state")
}
