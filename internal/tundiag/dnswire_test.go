package tundiag

import (
	"encoding/binary"
	"net"
	"testing"
)

func TestDNSWireRoundTripAResponse(t *testing.T) {
	message := dnsFixtureResponse(t, 0x1234, "example.com", DNSRecordTypeA, DNSRCodeSuccess, net.ParseIP("192.0.2.10").To4())
	evidence, err := ParseDNSResponse(message, 0x1234, "example.com", DNSRecordTypeA)
	if err != nil { t.Fatal(err) }
	if evidence.ResponseCode != DNSRCodeSuccess || len(evidence.Addresses) != 1 || evidence.Addresses[0] != "192.0.2.10" {
		t.Fatalf("unexpected DNS evidence: %#v", evidence)
	}
}

func TestDNSWireRejectsMismatchedMessageIDAndCompressionLoop(t *testing.T) {
	message := dnsFixtureResponse(t, 7, "example.com", DNSRecordTypeA, DNSRCodeSuccess, net.ParseIP("192.0.2.10").To4())
	if _, err := ParseDNSResponse(message, 8, "example.com", DNSRecordTypeA); err == nil { t.Fatal("expected mismatched message id to fail") }
	loop := make([]byte, 18)
	binary.BigEndian.PutUint16(loop[0:2], 9)
	binary.BigEndian.PutUint16(loop[2:4], 0x8180)
	binary.BigEndian.PutUint16(loop[4:6], 1)
	loop[12], loop[13] = 0xc0, 0x0c
	binary.BigEndian.PutUint16(loop[14:16], DNSRecordTypeA)
	binary.BigEndian.PutUint16(loop[16:18], DNSClassIN)
	if _, err := ParseDNSResponse(loop, 9, "example.com", DNSRecordTypeA); err == nil { t.Fatal("expected compression loop to fail") }
}

func dnsFixtureResponse(t *testing.T, id uint16, name string, recordType uint16, rcode int, address []byte) []byte {
	t.Helper()
	query, err := BuildDNSQuery(id, name, recordType)
	if err != nil { t.Fatal(err) }
	message := append([]byte(nil), query...)
	binary.BigEndian.PutUint16(message[2:4], uint16(0x8180|rcode))
	if address == nil { return message }
	binary.BigEndian.PutUint16(message[6:8], 1)
	message = append(message, 0xc0, 0x0c)
	message = binary.BigEndian.AppendUint16(message, recordType)
	message = binary.BigEndian.AppendUint16(message, DNSClassIN)
	message = binary.BigEndian.AppendUint32(message, 60)
	message = binary.BigEndian.AppendUint16(message, uint16(len(address)))
	message = append(message, address...)
	return message
}
