package tundiag

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
)

const (
	DNSRecordTypeA     = uint16(1)
	DNSRecordTypeCNAME = uint16(5)
	DNSRecordTypeAAAA  = uint16(28)
	DNSClassIN         = uint16(1)
	DNSRCodeSuccess    = 0
	DNSRCodeNameError  = 3
)

type dnsAnswerRecord struct {
	owner       string
	recordType  uint16
	recordClass uint16
	dataOffset  int
	dataLength  int
}

type dnsAddressRecord struct {
	owner   string
	address string
}

func BuildDNSQuery(id uint16, name string, recordType uint16) ([]byte, error) {
	encodedName, err := encodeDNSName(name)
	if err != nil {
		return nil, err
	}
	message := make([]byte, 12, 12+len(encodedName)+4)
	binary.BigEndian.PutUint16(message[0:2], id)
	binary.BigEndian.PutUint16(message[2:4], 0x0100)
	binary.BigEndian.PutUint16(message[4:6], 1)
	message = append(message, encodedName...)
	message = binary.BigEndian.AppendUint16(message, recordType)
	message = binary.BigEndian.AppendUint16(message, DNSClassIN)
	return message, nil
}

func ParseDNSResponse(message []byte, expectedID uint16, expectedName string, expectedType uint16) (DNSEvidence, error) {
	if len(message) < 12 {
		return DNSEvidence{}, errors.New("DNS response is shorter than the 12-byte header")
	}
	id := binary.BigEndian.Uint16(message[0:2])
	flags := binary.BigEndian.Uint16(message[2:4])
	questionCount := int(binary.BigEndian.Uint16(message[4:6]))
	answerCount := int(binary.BigEndian.Uint16(message[6:8]))
	if id != expectedID {
		return DNSEvidence{}, fmt.Errorf("DNS response id %d does not match query id %d", id, expectedID)
	}
	if flags&0x8000 == 0 {
		return DNSEvidence{}, errors.New("DNS message is not a response")
	}
	if questionCount != 1 {
		return DNSEvidence{}, fmt.Errorf("DNS response has %d questions; expected 1", questionCount)
	}

	offset := 12
	questionName, next, err := readDNSName(message, offset, 0)
	if err != nil {
		return DNSEvidence{}, fmt.Errorf("decode DNS question name: %w", err)
	}
	offset = next
	if offset+4 > len(message) {
		return DNSEvidence{}, errors.New("DNS question is truncated")
	}
	questionType := binary.BigEndian.Uint16(message[offset : offset+2])
	questionClass := binary.BigEndian.Uint16(message[offset+2 : offset+4])
	offset += 4
	if normalizeDNSName(questionName) != normalizeDNSName(expectedName) {
		return DNSEvidence{}, fmt.Errorf("DNS response question %q does not match %q", questionName, expectedName)
	}
	if questionType != expectedType || questionClass != DNSClassIN {
		return DNSEvidence{}, fmt.Errorf("DNS response question type/class is %d/%d; expected %d/%d", questionType, questionClass, expectedType, DNSClassIN)
	}

	records := make([]dnsAnswerRecord, 0, answerCount)
	for i := 0; i < answerCount; i++ {
		owner, next, err := readDNSName(message, offset, 0)
		if err != nil {
			return DNSEvidence{}, fmt.Errorf("decode DNS answer %d name: %w", i, err)
		}
		offset = next
		if offset+10 > len(message) {
			return DNSEvidence{}, fmt.Errorf("DNS answer %d header is truncated", i)
		}
		recordType := binary.BigEndian.Uint16(message[offset : offset+2])
		recordClass := binary.BigEndian.Uint16(message[offset+2 : offset+4])
		dataLength := int(binary.BigEndian.Uint16(message[offset+8 : offset+10]))
		offset += 10
		if offset+dataLength > len(message) {
			return DNSEvidence{}, fmt.Errorf("DNS answer %d data is truncated", i)
		}
		records = append(records, dnsAnswerRecord{
			owner:       normalizeDNSName(owner),
			recordType:  recordType,
			recordClass: recordClass,
			dataOffset:  offset,
			dataLength:  dataLength,
		})
		offset += dataLength
	}

	evidence := DNSEvidence{
		Name:         normalizeDNSName(expectedName),
		Type:         expectedType,
		ResponseCode: int(flags & 0x000f),
		MessageID:    id,
	}
	cnameTargets := make(map[string]string)
	addressRecords := make([]dnsAddressRecord, 0, answerCount)
	for i, record := range records {
		if record.recordClass != DNSClassIN {
			continue
		}
		data := message[record.dataOffset : record.dataOffset+record.dataLength]
		switch record.recordType {
		case DNSRecordTypeCNAME:
			target, next, err := readDNSName(message, record.dataOffset, 0)
			if err != nil {
				return DNSEvidence{}, fmt.Errorf("decode DNS answer %d CNAME target: %w", i, err)
			}
			if next != record.dataOffset+record.dataLength {
				return DNSEvidence{}, fmt.Errorf("DNS answer %d CNAME data length does not match encoded name", i)
			}
			target = normalizeDNSName(target)
			if record.owner == "" || target == "" {
				return DNSEvidence{}, fmt.Errorf("DNS answer %d contains an empty CNAME owner or target", i)
			}
			if existing, ok := cnameTargets[record.owner]; ok && existing != target {
				return DNSEvidence{}, fmt.Errorf("DNS owner %q has conflicting CNAME targets %q and %q", record.owner, existing, target)
			}
			cnameTargets[record.owner] = target
		case expectedType:
			address, err := parseDNSAddress(record.recordType, data)
			if err != nil {
				return DNSEvidence{}, fmt.Errorf("decode DNS answer %d address: %w", i, err)
			}
			if address != "" {
				addressRecords = append(addressRecords, dnsAddressRecord{owner: record.owner, address: address})
			}
		}
	}

	if evidence.ResponseCode != DNSRCodeSuccess {
		return evidence, nil
	}
	reachable, err := validatedDNSCNAMEChain(evidence.Name, cnameTargets)
	if err != nil {
		return DNSEvidence{}, err
	}
	seenAddresses := make(map[string]struct{})
	for _, record := range addressRecords {
		if _, ok := reachable[record.owner]; !ok {
			continue
		}
		if _, duplicate := seenAddresses[record.address]; duplicate {
			continue
		}
		seenAddresses[record.address] = struct{}{}
		evidence.Addresses = append(evidence.Addresses, record.address)
	}
	if answerCount > 0 && len(evidence.Addresses) == 0 {
		return evidence, fmt.Errorf("DNS response contains no IN type %d answer for %q or its validated CNAME chain", expectedType, evidence.Name)
	}
	return evidence, nil
}

func parseDNSAddress(recordType uint16, data []byte) (string, error) {
	switch recordType {
	case DNSRecordTypeA:
		if len(data) != net.IPv4len {
			return "", fmt.Errorf("A record has %d bytes; expected %d", len(data), net.IPv4len)
		}
		return net.IP(data).String(), nil
	case DNSRecordTypeAAAA:
		if len(data) != net.IPv6len {
			return "", fmt.Errorf("AAAA record has %d bytes; expected %d", len(data), net.IPv6len)
		}
		return net.IP(data).String(), nil
	default:
		return "", nil
	}
}

func validatedDNSCNAMEChain(expectedName string, cnameTargets map[string]string) (map[string]struct{}, error) {
	reachable := make(map[string]struct{})
	current := normalizeDNSName(expectedName)
	for {
		if _, exists := reachable[current]; exists {
			return nil, fmt.Errorf("DNS CNAME chain for %q contains a cycle at %q", expectedName, current)
		}
		reachable[current] = struct{}{}
		target, ok := cnameTargets[current]
		if !ok {
			return reachable, nil
		}
		current = target
	}
}

func encodeDNSName(name string) ([]byte, error) {
	name = normalizeDNSName(name)
	if name == "" {
		return nil, errors.New("DNS name is empty")
	}
	labels := strings.Split(name, ".")
	encoded := make([]byte, 0, len(name)+2)
	for _, label := range labels {
		if label == "" {
			return nil, fmt.Errorf("DNS name %q contains an empty label", name)
		}
		if len(label) > 63 {
			return nil, fmt.Errorf("DNS label %q exceeds 63 bytes", label)
		}
		encoded = append(encoded, byte(len(label)))
		encoded = append(encoded, label...)
	}
	encoded = append(encoded, 0)
	if len(encoded) > 255 {
		return nil, fmt.Errorf("DNS name %q exceeds 255 encoded bytes", name)
	}
	return encoded, nil
}

func readDNSName(message []byte, offset, depth int) (string, int, error) {
	if depth > 16 {
		return "", 0, errors.New("DNS compression pointer depth exceeded")
	}
	if offset < 0 || offset >= len(message) {
		return "", 0, errors.New("DNS name offset is outside the message")
	}
	labels := make([]string, 0, 4)
	next := offset
	jumped := false
	visited := make(map[int]struct{})
	for {
		if offset >= len(message) {
			return "", 0, errors.New("DNS name is truncated")
		}
		if _, exists := visited[offset]; exists {
			return "", 0, errors.New("DNS compression pointer loop detected")
		}
		visited[offset] = struct{}{}
		length := int(message[offset])
		switch {
		case length == 0:
			if !jumped {
				next = offset + 1
			}
			return strings.Join(labels, "."), next, nil
		case length&0xc0 == 0xc0:
			if offset+1 >= len(message) {
				return "", 0, errors.New("DNS compression pointer is truncated")
			}
			pointer := ((length & 0x3f) << 8) | int(message[offset+1])
			if pointer >= len(message) {
				return "", 0, errors.New("DNS compression pointer is outside the message")
			}
			if !jumped {
				next = offset + 2
				jumped = true
			}
			pointedName, _, err := readDNSName(message, pointer, depth+1)
			if err != nil {
				return "", 0, err
			}
			if pointedName != "" {
				labels = append(labels, strings.Split(pointedName, ".")...)
			}
			return strings.Join(labels, "."), next, nil
		case length&0xc0 != 0:
			return "", 0, fmt.Errorf("unsupported DNS label prefix 0x%x", length)
		default:
			offset++
			if length > 63 || offset+length > len(message) {
				return "", 0, errors.New("DNS label is truncated or invalid")
			}
			labels = append(labels, string(message[offset:offset+length]))
			offset += length
			if !jumped {
				next = offset
			}
		}
	}
}

func normalizeDNSName(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}
