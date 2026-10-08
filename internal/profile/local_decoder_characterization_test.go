package profile

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestLocalImportDetectionPrecedenceCharacterization(t *testing.T) {
	uri := "vless://00000000-0000-0000-0000-000000000001@example.com:443?type=tcp&security=tls#stable"
	for _, tt := range []struct {
		name    string
		content string
		format  LocalImportFormat
		wantErr string
	}{
		{name: "plain URI first", content: uri, format: LocalImportFormatURIList},
		{name: "base64 after plain URI", content: base64.StdEncoding.EncodeToString([]byte(uri)), format: LocalImportFormatBase64URIList},
		{name: "JSON object locks detection", content: `{"outbounds":`, wantErr: "malformed Xray JSON"},
		{name: "JSON scalar locks detection", content: `false`, wantErr: "top-level type boolean"},
		{name: "unsupported entry without a profile", content: "hysteria2://example.com:443", wantErr: "no supported profiles"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ImportLocalContent([]byte(tt.content))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || result.Format != tt.format || len(result.Profiles) != 1 {
				t.Fatalf("result = %+v, err = %v", result, err)
			}
		})
	}
}
