package daemon

import (
	"context"
	"io"
	"net"
	"testing"

	"github.com/AidarKhusainov/tunwarden/internal/network/planner"
)

func TestVerifyTunConnectivityUsesPrivateSocksEndpoint(t *testing.T) {
	endpoint, stop := startFakeSOCKSServer(t, 0x00)
	defer stop()

	err := verifyTunConnectivity(context.Background(), planner.TunPlan{TunDevice: planner.TunDevicePlan{Name: "tunwarden0"}}, tunCoreRuntimePlan{SOCKSEndpoint: endpoint})
	if err != nil {
		t.Fatalf("expected connectivity probe to pass, got %v", err)
	}
}

func TestVerifyTunConnectivityFailsWhenSocksConnectFails(t *testing.T) {
	endpoint, stop := startFakeSOCKSServer(t, 0x05)
	defer stop()

	err := verifyTunConnectivity(context.Background(), planner.TunPlan{TunDevice: planner.TunDevicePlan{Name: "tunwarden0"}}, tunCoreRuntimePlan{SOCKSEndpoint: endpoint})
	if err == nil {
		t.Fatal("expected connectivity probe to fail")
	}
}

func startFakeSOCKSServer(t *testing.T, connectStatus byte) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		greeting := make([]byte, 3)
		if _, err := io.ReadFull(conn, greeting); err != nil {
			return
		}
		_, _ = conn.Write([]byte{0x05, 0x00})
		request := make([]byte, 10)
		if _, err := io.ReadFull(conn, request); err != nil {
			return
		}
		_, _ = conn.Write([]byte{0x05, connectStatus, 0x00, 0x01, 127, 0, 0, 1, 0x1f, 0x90})
	}()
	return listener.Addr().String(), func() {
		_ = listener.Close()
		<-done
	}
}
