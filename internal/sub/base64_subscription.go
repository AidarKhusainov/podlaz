package sub

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/profile"
)

func ParseBase64Subscription(content []byte) (Parsed, error) {
	decoded, err := decodeBase64Flexible(string(content))
	if err != nil {
		return Parsed{}, fmt.Errorf("parse Base64 subscription: %w", err)
	}

	return parseURIListEntries(decoded, false)
}

// parsePlainURIListSubscription shares the established URI validation and identity path.
func parsePlainURIListSubscription(content []byte) (Parsed, error) {
	return parseURIListEntries(content, true)
}

func parseURIListEntries(decoded []byte, redactErrors bool) (Parsed, error) {
	var parsed Parsed
	seenProfiles := map[string]struct{}{}
	lines := strings.Split(strings.ReplaceAll(string(decoded), "\r\n", "\n"), "\n")
	for i, rawLine := range lines {
		lineNo := i + 1
		entry := strings.TrimSpace(rawLine)
		if entry == "" {
			continue
		}
		p, warnings, err := importEntry(entry)
		if err != nil {
			message := err.Error()
			if redactErrors {
				message = "invalid or unsupported share URI"
			}
			parsed.Unsupported = append(parsed.Unsupported, Issue{Line: lineNo, Message: message})
			continue
		}
		if _, duplicate := seenProfiles[p.ID]; duplicate {
			parsed.Unsupported = append(parsed.Unsupported, Issue{Line: lineNo, Message: fmt.Sprintf("duplicate profile id %q ignored", p.ID)})
			continue
		}
		seenProfiles[p.ID] = struct{}{}
		parsed.Profiles = append(parsed.Profiles, p)
		for _, warning := range warnings {
			parsed.Warnings = append(parsed.Warnings, Issue{Line: lineNo, Message: warning})
		}
	}
	if len(parsed.Profiles) == 0 {
		if len(parsed.Unsupported) > 0 {
			return Parsed{}, fmt.Errorf("subscription contains no supported profiles; first unsupported entry on line %d: %s", parsed.Unsupported[0].Line, parsed.Unsupported[0].Message)
		}
		return Parsed{}, fmt.Errorf("subscription contains no supported profiles")
	}
	profile.DeduplicateDisplayNames(parsed.Profiles)
	sort.SliceStable(parsed.Profiles, func(i, j int) bool { return parsed.Profiles[i].ID < parsed.Profiles[j].ID })
	return parsed, nil
}

func decodeBase64Flexible(raw string) ([]byte, error) {
	compact := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, raw)
	if compact == "" {
		return nil, fmt.Errorf("content is empty")
	}
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	var lastErr error
	for _, enc := range encodings {
		decoded, err := enc.DecodeString(compact)
		if err == nil {
			return decoded, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func importEntry(entry string) (profile.Profile, []string, error) {
	u, err := url.Parse(entry)
	if err != nil {
		return profile.Profile{}, nil, fmt.Errorf("invalid URI: %w", err)
	}
	if u.Scheme == "" {
		return profile.Profile{}, nil, fmt.Errorf("unsupported URI entry: scheme is required")
	}
	p, warnings, err := profile.ImportShareURI(entry)
	if err != nil {
		return profile.Profile{}, nil, err
	}
	p.Source = profile.SourceSubscription
	if err := profile.Validate(p); err != nil {
		return profile.Profile{}, nil, err
	}
	return p, warnings, nil
}
