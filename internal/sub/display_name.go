package sub

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/AidarKhusainov/tunwarden/internal/profile"
)

const SubscriptionDisplayNameRejectedWarning = "provider subscription display name was rejected; using safe fallback"

// ProviderSubscriptionDisplayName extracts a subscription-level display name from
// known, tested provider metadata fields. It intentionally ignores entry-level
// profile names and provider error/unsupported-client messages.
func ProviderSubscriptionDisplayName(format Format, content []byte) (string, []Issue) {
	if format != FormatXrayJSON {
		return "", nil
	}
	trimmed := bytes.TrimSpace(content)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return "", nil
	}

	object, err := decodeSubscriptionJSONObject(trimmed)
	if err != nil {
		return "", nil
	}
	if _, unsupported := unsupportedClientProviderMessage(object); unsupported {
		return "", nil
	}

	for _, key := range []string{"title", "name", "remarks"} {
		raw, ok := object[key]
		if !ok {
			continue
		}
		value, ok := rawJSONString(raw)
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		if name, ok := profile.SanitizeDisplayName(value); ok {
			return name, nil
		}
		return "", []Issue{{Line: 1, Message: SubscriptionDisplayNameRejectedWarning}}
	}
	return "", nil
}

func decodeSubscriptionJSONObject(content []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil {
		return nil, fmt.Errorf("malformed Xray JSON subscription object: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("malformed Xray JSON subscription object: trailing data")
	}
	return object, nil
}

// RefreshProviderDisplayName updates provider-owned subscription display names
// while keeping the subscription ID as the durable command-facing identity.
func RefreshProviderDisplayName(source Source, providerName string) Source {
	if strings.TrimSpace(providerName) == "" {
		return source
	}
	if source.ID != profile.NormalizeID(fallbackSubscriptionDisplayName(source.URL)) {
		return source
	}
	name, ok := profile.SanitizeDisplayName(providerName)
	if !ok {
		return source
	}
	source.Name = name
	return source
}
