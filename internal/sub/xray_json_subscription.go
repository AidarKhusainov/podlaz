package sub

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/AidarKhusainov/tunwarden/internal/profile"
)

// ParseSubscriptionContent detects and parses supported subscription response
// formats. JSON detection is intentionally based on the trimmed first byte so
// providers with generic or incorrect Content-Type headers are still handled
// correctly.
func ParseSubscriptionContent(content []byte) (Format, Parsed, error) {
	trimmed := bytes.TrimSpace(content)
	if len(trimmed) == 0 {
		return FormatUnknown, Parsed{}, fmt.Errorf("subscription content is empty")
	}
	switch trimmed[0] {
	case '{', '[':
		parsed, err := ParseXrayJSONSubscription(trimmed)
		return FormatXrayJSON, parsed, err
	default:
		if json.Valid(trimmed) {
			var value any
			decoder := json.NewDecoder(bytes.NewReader(trimmed))
			decoder.UseNumber()
			if err := decoder.Decode(&value); err == nil {
				return FormatXrayJSON, Parsed{}, fmt.Errorf("unsupported subscription JSON top-level type %s; expected Xray JSON object or array", subscriptionJSONTopLevelType(value))
			}
		}
		parsed, err := ParseBase64Subscription(content)
		return FormatBase64, parsed, err
	}
}

// ParseXrayJSONSubscription parses remote Xray JSON subscription content into
// normalized subscription-owned profiles. Raw Xray configuration is never
// returned as persistent source of truth.
func ParseXrayJSONSubscription(content []byte) (Parsed, error) {
	trimmed := bytes.TrimSpace(content)
	if len(trimmed) == 0 {
		return Parsed{}, fmt.Errorf("Xray JSON subscription is empty")
	}
	switch trimmed[0] {
	case '{':
		return parseXrayJSONObjectSubscription(trimmed)
	case '[':
		return parseXrayJSONArraySubscription(trimmed)
	default:
		var value any
		decoder := json.NewDecoder(bytes.NewReader(trimmed))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return Parsed{}, fmt.Errorf("malformed Xray JSON subscription: %w", err)
		}
		return Parsed{}, fmt.Errorf("unsupported subscription JSON top-level type %s; expected Xray JSON object or array", subscriptionJSONTopLevelType(value))
	}
}

func parseXrayJSONObjectSubscription(content []byte) (Parsed, error) {
	if err := rejectUnsupportedClientXrayJSONObject(content); err != nil {
		return Parsed{}, err
	}
	local, err := profile.ImportLocalContent(content)
	if err != nil {
		return Parsed{}, fmt.Errorf("parse Xray JSON subscription: %w", err)
	}
	if local.Format != profile.LocalImportFormatXrayJSON {
		return Parsed{}, fmt.Errorf("unsupported Xray JSON subscription: detected local format %q", local.Format)
	}
	return parsedFromLocalXrayResult(local)
}

func rejectUnsupportedClientXrayJSONObject(content []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil {
		return fmt.Errorf("malformed Xray JSON subscription object: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("malformed Xray JSON subscription object: trailing data")
	}
	if _, ok := unsupportedClientProviderMessage(object); ok {
		return fmt.Errorf("unsupported Xray JSON subscription response: provider reports unsupported client")
	}
	return nil
}

func unsupportedClientProviderMessage(object map[string]json.RawMessage) (string, bool) {
	for _, key := range []string{"remarks", "remark", "message", "error"} {
		raw, ok := object[key]
		if !ok {
			continue
		}
		message, ok := rawJSONString(raw)
		if ok && isUnsupportedClientProviderMessage(message) {
			return message, true
		}
	}
	return "", false
}

func rawJSONString(raw json.RawMessage) (string, bool) {
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, true
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return "", false
	}
	for _, key := range []string{"message", "remarks", "remark", "error"} {
		if nested, ok := object[key]; ok {
			if value, ok := rawJSONString(nested); ok {
				return value, true
			}
		}
	}
	return "", false
}

func isUnsupportedClientProviderMessage(message string) bool {
	normalized := strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(normalized, "app not supported") ||
		strings.Contains(normalized, "application not supported") ||
		strings.Contains(normalized, "unsupported client") ||
		strings.Contains(normalized, "client not supported")
}

func parseXrayJSONArraySubscription(content []byte) (Parsed, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	var rawEntries []json.RawMessage
	if err := decoder.Decode(&rawEntries); err != nil {
		return Parsed{}, fmt.Errorf("malformed Xray JSON subscription array: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Parsed{}, fmt.Errorf("malformed Xray JSON subscription array: trailing data")
	}
	if len(rawEntries) == 0 {
		return Parsed{}, fmt.Errorf("Xray JSON subscription array contains no entries")
	}

	parsed := Parsed{}
	seen := map[string]struct{}{}
	for i, raw := range rawEntries {
		entry := i + 1
		if !looksLikeJSONObject(raw) {
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				parsed.Unsupported = append(parsed.Unsupported, Issue{Line: entry, Message: fmt.Sprintf("malformed Xray JSON array entry: %v", err)})
				continue
			}
			parsed.Unsupported = append(parsed.Unsupported, Issue{Line: entry, Message: fmt.Sprintf("unsupported Xray JSON array entry type %s; expected object", subscriptionJSONTopLevelType(value))})
			continue
		}

		candidate, err := parseXrayJSONObjectSubscription(raw)
		if err != nil {
			parsed.Unsupported = append(parsed.Unsupported, Issue{Line: entry, Message: err.Error()})
			continue
		}
		for _, p := range candidate.Profiles {
			if _, duplicate := seen[p.ID]; duplicate {
				return Parsed{}, fmt.Errorf("duplicate subscription profile id %q", p.ID)
			}
			seen[p.ID] = struct{}{}
			parsed.Profiles = append(parsed.Profiles, p)
		}
		for _, issue := range candidate.Unsupported {
			parsed.Unsupported = append(parsed.Unsupported, Issue{Line: entry, Message: issue.Message})
		}
		for _, warning := range candidate.Warnings {
			parsed.Warnings = append(parsed.Warnings, Issue{Line: entry, Message: warning.Message})
		}
	}
	if len(parsed.Profiles) == 0 {
		if len(parsed.Unsupported) > 0 {
			return Parsed{}, fmt.Errorf("Xray JSON subscription contains no supported profiles; first unsupported entry %d: %s", parsed.Unsupported[0].Line, parsed.Unsupported[0].Message)
		}
		return Parsed{}, fmt.Errorf("Xray JSON subscription contains no supported profiles")
	}
	profile.DeduplicateDisplayNames(parsed.Profiles)
	sort.SliceStable(parsed.Profiles, func(i, j int) bool { return parsed.Profiles[i].ID < parsed.Profiles[j].ID })
	return parsed, nil
}

func parsedFromLocalXrayResult(local profile.LocalImportResult) (Parsed, error) {
	parsed := Parsed{
		Profiles:    make([]profile.Profile, 0, len(local.Profiles)),
		Unsupported: make([]Issue, 0, len(local.Unsupported)),
		Warnings:    make([]Issue, 0, len(local.Warnings)),
	}
	seen := map[string]struct{}{}
	for _, p := range local.Profiles {
		p.Source = profile.SourceSubscription
		if err := profile.Validate(p); err != nil {
			return Parsed{}, err
		}
		if _, duplicate := seen[p.ID]; duplicate {
			return Parsed{}, fmt.Errorf("duplicate subscription profile id %q", p.ID)
		}
		seen[p.ID] = struct{}{}
		parsed.Profiles = append(parsed.Profiles, p)
	}
	for _, issue := range local.Unsupported {
		parsed.Unsupported = append(parsed.Unsupported, Issue{Line: issue.Entry, Message: issue.Message})
	}
	for _, warning := range local.Warnings {
		parsed.Warnings = append(parsed.Warnings, Issue{Line: warning.Entry, Message: warning.Message})
	}
	if len(parsed.Profiles) == 0 {
		if len(parsed.Unsupported) > 0 {
			return Parsed{}, fmt.Errorf("Xray JSON subscription contains no supported profiles; first unsupported entry %d: %s", parsed.Unsupported[0].Line, parsed.Unsupported[0].Message)
		}
		return Parsed{}, fmt.Errorf("Xray JSON subscription contains no supported profiles")
	}
	profile.DeduplicateDisplayNames(parsed.Profiles)
	sort.SliceStable(parsed.Profiles, func(i, j int) bool { return parsed.Profiles[i].ID < parsed.Profiles[j].ID })
	return parsed, nil
}

func looksLikeJSONObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

func subscriptionJSONTopLevelType(value any) string {
	switch value.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case json.Number, float64:
		return "number"
	case bool:
		return "boolean"
	case nil:
		return "null"
	default:
		return "value"
	}
}
