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
	_, err = requestNMConfinedXrayGeneration(context.Background(), fd, nmBrokerGenerationRequest{}, nil)
	if err == nil {
		t.Fatal("untrusted broker request unexpectedly succeeded")
	}
	if _, err := fd.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("refused broker request retained TUN candidate descriptor: %v", err)
	}
}

func TestNMClientBindsContentDigestOnlyToOfficialPreflightProof(t *testing.T) {
	prepared := &nmOwnedXrayPrepared{
		config: []byte("{}"), interfaceName: "podlaz0", mtu: 1500, mark: 101,
	}
	prepared.digest = nmXrayAuthorityDigest(prepared.config, prepared.interfaceName, prepared.mtu, prepared.mark)
	request := nmBrokerGenerationRequest{
		Schema:     nmBrokerRequestSchema,
		EgressMark: 101,
		Kernel:     nmTunFDKernelIdentity{Name: "podlaz0", MTU: 1500},
	}
	bound, err := bindNMPreparedBrokerRequest(request, prepared)
	if err != nil || bound.ConfigSHA256 != prepared.configSHA256() {
		t.Fatalf("official config digest not bound to broker request: %v", err)
	}
	for _, tc := range []struct {
		name string
		edit func(*nmBrokerGenerationRequest, *nmOwnedXrayPrepared)
	}{
		{"wrong mark", func(r *nmBrokerGenerationRequest, _ *nmOwnedXrayPrepared) { r.EgressMark++ }},
		{"wrong MTU", func(r *nmBrokerGenerationRequest, _ *nmOwnedXrayPrepared) { r.Kernel.MTU++ }},
		{"wrong interface", func(r *nmBrokerGenerationRequest, _ *nmOwnedXrayPrepared) { r.Kernel.Name = "other0" }},
		{"untrusted hash", func(r *nmBrokerGenerationRequest, _ *nmOwnedXrayPrepared) { r.ConfigSHA256 = nmBrokerTestDigest }},
		{"tampered preflight", func(_ *nmBrokerGenerationRequest, p *nmOwnedXrayPrepared) { p.config[0] = 'x' }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proof := *prepared
			proof.config = append([]byte(nil), prepared.config...)
			bad := request
			tc.edit(&bad, &proof)
			if _, err := bindNMPreparedBrokerRequest(bad, &proof); err == nil {
				t.Fatal("altered official preflight reached broker")
			}
		})
	}
}
