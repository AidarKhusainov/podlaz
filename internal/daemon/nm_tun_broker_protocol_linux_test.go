//go:build linux

package daemon

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestNMBrokerRequestFailsClosedForMissingOrAmbiguousAuthority(t *testing.T) {
	valid := nmBrokerGenerationRequest{
		Schema:     nmBrokerRequestSchema,
		SessionID:  "00112233445566778899aabbccddeeff",
		EgressMark: 101,
		ConfigPath: "/run/podlaz/generated/xray.json",
		Kernel:     testNMFDIdentity(),
		NM:         validExpectedNMTun(),
	}
	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeNMBrokerGenerationRequest(data)
	if err != nil || got != valid {
		t.Fatalf("valid exact broker request refused: %v", err)
	}
	for _, tc := range []struct {
		name string
		edit func(*nmBrokerGenerationRequest)
	}{
		{"schema", func(r *nmBrokerGenerationRequest) { r.Schema = "foreign" }},
		{"session", func(r *nmBrokerGenerationRequest) { r.SessionID = "" }},
		{"mark", func(r *nmBrokerGenerationRequest) { r.EgressMark = 0 }},
		{"kernel", func(r *nmBrokerGenerationRequest) { r.Kernel.Index = 0 }},
		{"NM UUID", func(r *nmBrokerGenerationRequest) { r.NM.UUID = "" }},
		{"identity mismatch", func(r *nmBrokerGenerationRequest) { r.NM.Name = "foreign0" }},
		{"config relative", func(r *nmBrokerGenerationRequest) { r.ConfigPath = "xray.json" }},
		{"config traversal", func(r *nmBrokerGenerationRequest) { r.ConfigPath = "/run/../etc/xray.json" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := valid
			tc.edit(&bad)
			input, err := json.Marshal(bad)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeNMBrokerGenerationRequest(input); err == nil {
				t.Fatal("unsafe broker request admitted")
			}
		})
	}
	for _, extra := range []string{"{}", "null", "garbage"} {
		if _, err := decodeNMBrokerGenerationRequest([]byte(extra)); err == nil {
			t.Fatal("empty or corrupt broker request admitted")
		}
	}
	if _, err := decodeNMBrokerGenerationRequest(append(append([]byte(nil), data...), []byte("{}")...)); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	if _, err := decodeNMBrokerGenerationRequest(bytes.Repeat([]byte("x"), nmBrokerRequestMaxBytes+1)); err == nil {
		t.Fatal("oversized request admitted")
	}
	if _, err := decodeNMBrokerGenerationRequest([]byte(strings.TrimSuffix(string(data), "}") + `,"unsupported":true}`)); err == nil {
		t.Fatal("unknown broker request field admitted")
	}
}
