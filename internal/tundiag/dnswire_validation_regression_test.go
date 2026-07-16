package tundiag

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

const dnsTestRecordTypeCNAME = uint16(5)

type dnsTestAnswer struct {
	owner      string
	recordType uint16
	address    net.IP
	cname      string
}

func TestParseDNSResponseRejectsAddressForDifferentOwner(t *testing.T) {
	message := dnsFixtureResponseWithAnswers(t, 0x1001, "example.com", DNSRecordTypeA,
		dnsTestAnswer{owner: "other.example.com", recordType: DNSRecordTypeA, address: net.ParseIP("192.0.2.10").To4()},
	)
	if _, err := ParseDNSResponse(message, 0x1001, "example.com", DNSRecordTypeA); err == nil {
		t.Fatal("expected an address for a different owner to be rejected")
	}
}

func TestParseDNSResponseRejectsMismatchedAddressType(t *testing.T) {
	message := dnsFixtureResponseWithAnswers(t, 0x1002, "example.com", DNSRecordTypeA,
		dnsTestAnswer{owner: "example.com", recordType: DNSRecordTypeAAAA, address: net.ParseIP("2001:db8::10").To16()},
	)
	if _, err := ParseDNSResponse(message, 0x1002, "example.com", DNSRecordTypeA); err == nil {
		t.Fatal("expected an AAAA answer for an A query to be rejected")
	}
}

func TestParseDNSResponseRejectsAddressOutsideCNAMEChain(t *testing.T) {
	message := dnsFixtureResponseWithAnswers(t, 0x1003, "example.com", DNSRecordTypeA,
		dnsTestAnswer{owner: "example.com", recordType: dnsTestRecordTypeCNAME, cname: "edge.example.com"},
		dnsTestAnswer{owner: "unrelated.example.com", recordType: DNSRecordTypeA, address: net.ParseIP("192.0.2.20").To4()},
	)
	if _, err := ParseDNSResponse(message, 0x1003, "example.com", DNSRecordTypeA); err == nil {
		t.Fatal("expected an address outside the validated CNAME chain to be rejected")
	}
}

func TestParseDNSResponseAcceptsAddressThroughValidatedCNAMEChain(t *testing.T) {
	message := dnsFixtureResponseWithAnswers(t, 0x1004, "example.com", DNSRecordTypeA,
		dnsTestAnswer{owner: "example.com", recordType: dnsTestRecordTypeCNAME, cname: "edge.example.com"},
		dnsTestAnswer{owner: "edge.example.com", recordType: DNSRecordTypeA, address: net.ParseIP("192.0.2.30").To4()},
	)
	evidence, err := ParseDNSResponse(message, 0x1004, "example.com", DNSRecordTypeA)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence.Addresses) != 1 || evidence.Addresses[0] != "192.0.2.30" {
		t.Fatalf("unexpected CNAME-chain evidence: %#v", evidence)
	}
}

func TestParseDNSResponseRejectsInvalidHeaderFlags(t *testing.T) {
	const id = 0x1005
	base := dnsFixtureResponseWithAnswers(t, id, "example.com", DNSRecordTypeA,
		dnsTestAnswer{owner: "example.com", recordType: DNSRecordTypeA, address: net.ParseIP("192.0.2.40").To4()},
	)

	for name, flag := range map[string]uint16{
		"truncated":       0x0200,
		"non-query opcode": 0x0800,
		"non-zero z":      0x0010,
	} {
		t.Run(name, func(t *testing.T) {
			message := append([]byte(nil), base...)
			binary.BigEndian.PutUint16(message[2:4], binary.BigEndian.Uint16(message[2:4])|flag)
			if _, err := ParseDNSResponse(message, id, "example.com", DNSRecordTypeA); err == nil {
				t.Fatalf("expected DNS header flag 0x%04x to be rejected", flag)
			}
		})
	}
}

func TestParseDNSResponseRejectsCNAMEWithAddressAtSameOwner(t *testing.T) {
	tests := []struct {
		name       string
		id         uint16
		recordType uint16
		address    net.IP
	}{
		{name: "A", id: 0x1006, recordType: DNSRecordTypeA, address: net.ParseIP("192.0.2.50").To4()},
		{name: "AAAA", id: 0x1007, recordType: DNSRecordTypeAAAA, address: net.ParseIP("2001:db8::50").To16()},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message := dnsFixtureResponseWithAnswers(t, test.id, "example.com", test.recordType,
				dnsTestAnswer{owner: "example.com", recordType: dnsTestRecordTypeCNAME, cname: "edge.example.com"},
				dnsTestAnswer{owner: "example.com", recordType: test.recordType, address: test.address},
			)
			if _, err := ParseDNSResponse(message, test.id, "example.com", test.recordType); err == nil {
				t.Fatalf("expected CNAME plus %s at the same owner to be rejected", test.name)
			}
		})
	}
}

func TestNetworkClientDNSUDPRejectsTruncatedResponse(t *testing.T) {
	const id = 0x1008
	response := dnsFixtureResponseWithAnswers(t, id, "example.com", DNSRecordTypeA,
		dnsTestAnswer{owner: "example.com", recordType: DNSRecordTypeA, address: net.ParseIP("192.0.2.60").To4()},
	)
	binary.BigEndian.PutUint16(response[2:4], binary.BigEndian.Uint16(response[2:4])|0x0200)

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	go func() {
		buffer := make([]byte, 4096)
		if _, err := serverConn.Read(buffer); err != nil {
			return
		}
		_, _ = serverConn.Write(response)
	}()

	client := NetworkClient{
		DialContext: func(context.Context, string, string) (net.Conn, error) { return clientConn, nil },
		MessageID:   func() (uint16, error) { return id, nil },
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if evidence, err := client.DNSUDP(ctx, "192.0.2.53", "example.com", DNSRecordTypeA); err == nil {
		t.Fatalf("expected truncated UDP DNS response to fail, got %#v", evidence)
	}
}

func dnsFixtureResponseWithAnswers(t *testing.T, id uint16, name string, questionType uint16, answers ...dnsTestAnswer) []byte {
	t.Helper()
	message, err := BuildDNSQuery(id, name, questionType)
	if err != nil {
		t.Fatal(err)
	}
	binary.BigEndian.PutUint16(message[2:4], 0x8180)
	binary.BigEndian.PutUint16(message[6:8], uint16(len(answers)))

	for _, answer := range answers {
		owner, err := encodeDNSName(answer.owner)
		if err != nil {
			t.Fatal(err)
		}
		message = append(message, owner...)
		message = binary.BigEndian.AppendUint16(message, answer.recordType)
		message = binary.BigEndian.AppendUint16(message, DNSClassIN)
		message = binary.BigEndian.AppendUint32(message, 60)

		var data []byte
		switch answer.recordType {
		case dnsTestRecordTypeCNAME:
			data, err = encodeDNSName(answer.cname)
			if err != nil {
				t.Fatal(err)
			}
		default:
			data = append([]byte(nil), answer.address...)
		}
		message = binary.BigEndian.AppendUint16(message, uint16(len(data)))
		message = append(message, data...)
	}
	return message
}
