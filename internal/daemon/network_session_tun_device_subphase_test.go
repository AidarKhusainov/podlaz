package daemon

import (
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestValidNetworkSessionApplySubphaseAcceptsTunDevice(t *testing.T) {
	if !validNetworkSessionApplySubphase(api.NetworkSessionApplySubphaseTUNDevice) {
		t.Fatalf("tun-device replay subphase rejected")
	}
}
