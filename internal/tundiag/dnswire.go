package tundiag

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
)

const (
	DNSRecordTypeA    = uint16(1)
	DNSRecordTypeAAAA = uint16(28)
	DNSClassIN        = uint16(1)
	DNSRCodeSuccess   = 0
	DNSRCodeNameError = 3
)

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

	evidence := DNSEvidence{
		Name:         normalizeDNSName(expectedName),
		Type:         expectedType,
		ResponseCode: int(flags & 0x000f),
		MessageID:    id,
	}
	for i := 0; i < answerCount; i++ {
		_, next, err := readDNSName(message, offset, 0)
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
		if dataLength < 0 || offset+dataLength > len(message) {
			return DNSEvidence{}, fmt.Errorf("DNS answer %d data is truncated", i)
		}
		data := message[offset : offset+dataLength]
		offset += dataLength
		if recordClass != DNSClassIN {
			continue
		}
		switch {
		case recordType == DNSRecordTypeA && dataLength == net.IPv4len:
			evidence.Addresses = append(evidence.Addresses, net.IP(data).String())
		case recordType == DNSRecordTypeAAAA && dataLength == net.IPv6len:
			evidence.Addresses = append(evidence.Addresses, net.IP(data).String())
		}
	}
	return evidence, nil
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
