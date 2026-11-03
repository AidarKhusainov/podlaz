//go:build linux

package daemon

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestNMBrokerRejectsNonrootUnixPeer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broker.sock")
	listener, err := net.ListenUnix("unixpacket", &net.UnixAddr{Name: path, Net: "unixpacket"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.DialUnix("unixpacket", nil, &net.UnixAddr{Name: path, Net: "unixpacket"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	err = verifyNMBrokerPeer(server)
	if os.Geteuid() == 0 && err != nil {
		t.Fatalf("root Unix peer refused: %v", err)
	}
	if os.Geteuid() != 0 && err == nil {
		t.Fatal("ordinary Unix peer was admitted as privileged broker client")
	}
}

func TestNMClientRefusalConsumesSenderDescriptor(t *testing.T) {
	fd, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	_, err = requestNMConfinedXrayGeneration(context.Background(), fd, nmBrokerGenerationRequest{})
	if err == nil {
		t.Fatal("untrusted broker request unexpectedly succeeded")
	}
	if _, err := fd.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("refused broker request retained TUN candidate descriptor: %v", err)
	}
}
