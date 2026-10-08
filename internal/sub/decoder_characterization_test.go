package sub

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestSubscriptionFormatPrecedenceCharacterization(t *testing.T) {
	uri := entry("00000000-0000-0000-0000-000000000001", "example.com", "443", "?security=tls", "stable")
	tests := []struct {
		name    string
		content string
		format  Format
		wantErr string
		count   int
	}{
		{name: "base64 uri list", content: base64.StdEncoding.EncodeToString([]byte(uri)), format: FormatBase64, count: 1},
		{name: "malformed object is JSON", content: `{"outbounds":`, format: FormatXrayJSON, wantErr: "Xray JSON"},
		{name: "malformed array is JSON", content: `[{"outbounds":`, format: FormatXrayJSON, wantErr: "Xray JSON"},
		{name: "scalar JSON never falls through", content: `true`, format: FormatXrayJSON, wantErr: "top-level type boolean"},
		{name: "plain URI is not a subscription format", content: uri, format: FormatBase64, wantErr: "Base64"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			format, parsed, err := ParseSubscriptionContent([]byte(tt.content))
			if format != tt.format {
				t.Fatalf("format = %s, want %s", format, tt.format)
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || len(parsed.Profiles) != tt.count {
				t.Fatalf("parse: profiles=%d err=%v", len(parsed.Profiles), err)
			}
		})
	}
}
