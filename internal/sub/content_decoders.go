package sub

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/AidarKhusainov/podlaz/internal/profile"
)

// contentDecoder is an internal, ordered format adapter. Recognition is
// authoritative: once a decoder claims content, parse failure is not fallback.
type contentDecoder struct {
	recognizes func([]byte) bool
	local      func([]byte) (profile.LocalImportResult, error)
	remote     func([]byte) (Format, Parsed, error)
}

var importContentDecoders = []contentDecoder{
	{
		recognizes: recognizesXrayJSON,
		local:      profile.ImportLocalContent,
		remote: func(data []byte) (Format, Parsed, error) {
			parsed, err := ParseXrayJSONSubscription(data)
			return FormatXrayJSON, parsed, err
		},
	},
	{
		recognizes: recognizesMihomoYAML,
		local: func(data []byte) (profile.LocalImportResult, error) {
			return parseMihomoYAML(data, profile.SourceImportedFile)
		},
		remote: func(data []byte) (Format, Parsed, error) {
			local, err := parseMihomoYAML(data, profile.SourceSubscription)
			if err != nil {
				return FormatMihomo, Parsed{}, err
			}
			parsed := Parsed{Profiles: local.Profiles}
			for _, issue := range local.Unsupported {
				parsed.Unsupported = append(parsed.Unsupported, Issue{Line: issue.Entry, Message: issue.Message})
			}
			for _, issue := range local.Warnings {
				parsed.Warnings = append(parsed.Warnings, Issue{Line: issue.Entry, Message: issue.Message})
			}
			return FormatMihomo, parsed, nil
		},
	},
	{
		// Legacy fallbacks intentionally retain their separate historical
		// behavior: local URI-list then Base64; subscriptions Base64 only.
		recognizes: func([]byte) bool { return true },
		local:      profile.ImportLocalContent,
		remote: func(data []byte) (Format, Parsed, error) {
			parsed, err := ParseBase64Subscription(data)
			return FormatBase64, parsed, err
		},
	},
}

func recognizesXrayJSON(data []byte) bool {
	trimmed := bytes.TrimSpace(data)
	return len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[' || json.Valid(trimmed))
}

// ParseLocalImportContent uses the same format registration as subscriptions.
// The pre-existing local URI/Base64 parser remains the last decoder.
func ParseLocalImportContent(data []byte) (profile.LocalImportResult, error) {
	if len(data) > profile.MaxLocalImportSize {
		return profile.LocalImportResult{}, fmt.Errorf("local import file exceeds 4 MiB limit")
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return profile.LocalImportResult{}, fmt.Errorf("local import file is empty")
	}
	for _, decoder := range importContentDecoders {
		if decoder.recognizes(data) {
			return decoder.local(data)
		}
	}
	panic("missing local import fallback decoder")
}

func parseSubscriptionWithDecoders(data []byte) (Format, Parsed, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return FormatUnknown, Parsed{}, fmt.Errorf("subscription content is empty")
	}
	for _, decoder := range importContentDecoders {
		if decoder.recognizes(data) {
			return decoder.remote(data)
		}
	}
	panic("missing subscription fallback decoder")
}
