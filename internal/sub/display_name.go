package sub

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/profile"
)

const SubscriptionDisplayNameRejectedWarning = "provider subscription display name was rejected; using safe fallback"

// ProviderSubscriptionDisplayName extracts a subscription-level display name from
// known, tested provider metadata fields. It intentionally ignores entry-level
// profile names and provider error/unsupported-client messages.
func ProviderSubscriptionDisplayName(format Format, content []byte) (string, []Issue) {
	return ProviderSubscriptionDisplayNameFromMetadata(format, content, nil)
}

// ProviderSubscriptionDisplayNameFromMetadata extracts a subscription-level
// display name from safe HTTP response metadata first, then from wrapper-level
// JSON metadata.
func ProviderSubscriptionDisplayNameFromMetadata(format Format, content []byte, header http.Header) (string, []Issue) {
	candidates := subscriptionHeaderDisplayNameCandidates(header)
	if format == FormatXrayJSON {
		candidates = append(candidates, subscriptionJSONDisplayNameCandidates(content)...)
	}
	return firstSafeSubscriptionDisplayName(candidates)
}

func subscriptionHeaderDisplayNameCandidates(header http.Header) []string {
	if len(header) == 0 {
		return nil
	}
	var candidates []string
	if value := strings.TrimSpace(header.Get("Content-Disposition")); value != "" {
		_, params, err := mime.ParseMediaType(value)
		if err == nil {
			candidates = append(candidates, params["filename"], params["name"])
		}
	}
	for _, key := range []string{
		"Subscription-Title",
		"Profile-Title",
		"X-Subscription-Title",
		"X-Profile-Title",
		"Subscription-Name",
		"Profile-Name",
		"X-Subscription-Name",
		"X-Profile-Name",
	} {
		candidates = append(candidates, header.Get(key))
	}
	return candidates
}

func subscriptionJSONDisplayNameCandidates(content []byte) []string {
	trimmed := bytes.TrimSpace(content)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil
	}
	object, err := decodeSubscriptionJSONObject(trimmed)
	if err != nil {
		return nil
	}
	if _, unsupported := unsupportedClientProviderMessage(object); unsupported {
		return nil
	}

	var candidates []string
	for _, key := range []string{"title", "name", "remarks", "remark", "displayName", "display_name", "ps"} {
		raw, ok := object[key]
		if !ok {
			continue
		}
		value, ok := rawJSONString(raw)
		if ok {
			candidates = append(candidates, value)
		}
	}
	return candidates
}

func firstSafeSubscriptionDisplayName(candidates []string) (string, []Issue) {
	rejected := false
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if name, ok := profile.SanitizeDisplayName(candidate); ok {
			return name, nil
		}
		rejected = true
	}
	if rejected {
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
