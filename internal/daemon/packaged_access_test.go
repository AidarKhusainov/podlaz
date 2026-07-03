package daemon

import (
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestPackagedSocketGateRequiresSystemdServiceAndPeerCredentialAuthorizer(t *testing.T) {
	t.Setenv(api.ServiceEnv, api.ServiceSystemd)
	if !shouldListenOnAbstractSocket(PolkitAuthorizer{}) {
		t.Fatal("systemd packaged daemon with polkit should enable the abstract authorization socket")
	}
	if shouldListenOnAbstractSocket(AllowAuthorizer{}) {
		t.Fatal("packaged daemon should keep the abstract socket disabled without peer-credential authorization")
	}
}

func TestManualDaemonKeepsPackagedSocketDisabled(t *testing.T) {
	t.Setenv(api.ServiceEnv, api.ServiceManual)
	if shouldListenOnAbstractSocket(PolkitAuthorizer{}) {
		t.Fatal("manual daemon should keep the packaged abstract socket disabled")
	}
}
