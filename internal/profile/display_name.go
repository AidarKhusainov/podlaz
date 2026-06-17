package profile

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxDisplayNameRunes       = 80
	DisplayNameRejectedWarning = "provider display name was rejected; using safe fallback"
)

func SanitizeDisplayName(raw string) (string, bool) {
	name := strings.TrimSpace(strings.ToValidUTF8(raw, ""))
	if name == "" {
		return "", false
	}

	var b strings.Builder
	lastSpace := false
	for _, r := range name {
		switch {
		case r == '/' || r == '\\':
			if b.Len() > 0 && !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		case unicode.IsControl(r):
			if b.Len() > 0 && !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		case unicode.IsSpace(r):
			if b.Len() > 0 && !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		default:
			b.WriteRune(r)
			lastSpace = false
		}
	}
	name = strings.TrimSpace(b.String())
	if name == "" {
		return "", false
	}
	name = truncateRunes(name, MaxDisplayNameRunes)
	if unsafeDisplayName(name) {
		return "", false
	}
	return name, true
}

func ProviderProfileDisplayName(raw, protocol, host string, port uint16) (string, bool) {
	if name, ok := SanitizeDisplayName(raw); ok {
		return name, true
	}
	return FallbackProfileDisplayName(protocol, host, port), false
}

func FallbackProfileDisplayName(protocol, host string, port uint16) string {
	name := fmt.Sprintf("%s-%s-%d", strings.ToLower(strings.TrimSpace(protocol)), strings.TrimSpace(host), port)
	if sanitized, ok := SanitizeDisplayName(name); ok {
		return sanitized
	}
	protocol = NormalizeID(protocol)
	if protocol == "" {
		return "profile"
	}
	return protocol + "-profile"
}

func StableImportedProfileIDBase(protocol, host string, port uint16) string {
	base := NormalizeID(FallbackProfileDisplayName(protocol, host, port))
	if base == "" {
		base = NormalizeID(protocol) + "-profile"
	}
	return strings.Trim(base, "-._")
}

func DeduplicateDisplayNames(profiles []Profile) {
	seen := map[string]int{}
	for i := range profiles {
		base := strings.TrimSpace(profiles[i].Name)
		if base == "" {
			base = FallbackProfileDisplayName(profiles[i].Protocol, profiles[i].Server, profiles[i].Port)
		}
		key := strings.ToLower(base)
		seen[key]++
		if seen[key] == 1 {
			profiles[i].Name = base
			continue
		}
		suffix := " (" + strconv.Itoa(seen[key]) + ")"
		profiles[i].Name = truncateRunes(base, MaxDisplayNameRunes-utf8.RuneCountInString(suffix)) + suffix
	}
}

func truncateRunes(value string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	var b strings.Builder
	count := 0
	for _, r := range value {
		if count == max {
			break
		}
		b.WriteRune(r)
		count++
	}
	return strings.TrimSpace(b.String())
}

func unsafeDisplayName(value string) bool {
	v := strings.ToLower(strings.TrimSpace(value))
	if v == "" {
		return true
	}
	if uuidPattern.MatchString(v) || looksSecretLike(v) {
		return true
	}
	if strings.Contains(v, "://") || strings.HasPrefix(v, "vless:") || strings.HasPrefix(v, "vmess:") || strings.HasPrefix(v, "trojan:") || strings.HasPrefix(v, "ss:") {
		return true
	}
	if (strings.HasPrefix(v, "{") || strings.HasPrefix(v, "[")) && (strings.Contains(v, "outbounds") || strings.Contains(v, "inbounds")) {
		return true
	}
	return looksOpaqueTokenLike(value)
}

func looksOpaqueTokenLike(value string) bool {
	compact := strings.TrimSpace(value)
	if utf8.RuneCountInString(compact) < 32 || strings.ContainsAny(compact, " \t\n\r") {
		return false
	}
	for _, r := range compact {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._~+/=-", r) {
			continue
		}
		return false
	}
	return true
}
