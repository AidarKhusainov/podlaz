//go:build linux

package daemon

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func sendNMBrokerTestMessage(t *testing.T, additionalFDs int) (*net.UnixConn, *net.UnixListener) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "broker.sock")
	listener, err := net.ListenUnix("unixpacket", &net.UnixAddr{Name: path, Net: "unixpacket"})
	if err != nil {
		t.Fatal(err)
	}
	client, err := net.DialUnix("unixpacket", nil, &net.UnixAddr{Name: path, Net: "unixpacket"})
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	req := nmBrokerGenerationRequest{
		Schema: nmBrokerRequestSchema, SessionID: "00112233445566778899aabbccddeeff",
		EgressMark: 101, ConfigPath: "/run/podlaz/generated/xray.json", ConfigSHA256: nmBrokerTestDigest,
		Kernel: testNMFDIdentity(), NM: validExpectedNMTun(),
	}
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	files := make([]*os.File, 0, additionalFDs)
	fds := make([]int, 0, additionalFDs)
	for range additionalFDs {
		fd, err := os.Open("/dev/null")
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, fd)
		fds = append(fds, int(fd.Fd()))
	}
	t.Cleanup(func() {
		for _, file := range files {
			file.Close()
		}
		client.Close()
		listener.Close()
	})
	var rights []byte
	if len(fds) != 0 {
		rights = unix.UnixRights(fds...)
	}
	if _, _, err := client.WriteMsgUnix(payload, rights, nil); err != nil {
		t.Fatal(err)
	}
	return client, listener
}

func TestNMBrokerRejectsZeroAndMultipleDescriptors(t *testing.T) {
	for _, num := range []int{0, 2, 9} {
		t.Run(string(rune('0'+num)), func(t *testing.T) {
			_, listener := sendNMBrokerTestMessage(t, num)
			peer, err := listener.AcceptUnix()
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			_, fd, err := receiveNMBrokerRequest(peer)
			if fd != nil {
				fd.Close()
				t.Fatal("ambiguous descriptor count admitted")
			}
			if err == nil {
				t.Fatal("missing or extra descriptors admitted")
			}
		})
	}
}

func TestNMBrokerReceivesExactlyOneDescriptorAndAuthority(t *testing.T) {
	_, listener := sendNMBrokerTestMessage(t, 1)
	peer, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	request, fd, err := receiveNMBrokerRequest(peer)
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close()
	if request.Schema != nmBrokerRequestSchema || request.Kernel.Index <= 0 || request.NM.UUID == "" {
		t.Fatal("NM broker failed exact request authority")
	}
	info, err := fd.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		t.Fatalf("broker did not receive opened FD: %v", err)
	}
}
