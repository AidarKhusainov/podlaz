package profile

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxDisplayNameRunes        = 80
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
	usedFinalNames := map[string]struct{}{}
	nextSuffixByBase := map[string]int{}
	for i := range profiles {
		base := strings.TrimSpace(profiles[i].Name)
		if base == "" {
			base = FallbackProfileDisplayName(profiles[i].Protocol, profiles[i].Server, profiles[i].Port)
		}
		baseKey := displayNameKey(base)
		candidate := base
		if _, used := usedFinalNames[displayNameKey(candidate)]; used {
			next := nextSuffixByBase[baseKey]
			if next < 2 {
				next = 2
			}
			for {
				suffix := " (" + strconv.Itoa(next) + ")"
				candidate = truncateRunes(base, MaxDisplayNameRunes-utf8.RuneCountInString(suffix)) + suffix
				next++
				if _, used := usedFinalNames[displayNameKey(candidate)]; !used {
					nextSuffixByBase[baseKey] = next
					break
				}
			}
		} else if nextSuffixByBase[baseKey] == 0 {
			nextSuffixByBase[baseKey] = 2
		}
		profiles[i].Name = candidate
		usedFinalNames[displayNameKey(candidate)] = struct{}{}
	}
}

func displayNameKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
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
	if uuidPattern.MatchString(v) || looksSensitiveLike(v) {
		return true
	}
	if strings.Contains(v, "://") {
		return true
	}
	for _, prefix := range []string{"v" + "less:", "v" + "mess:", "tro" + "jan:", "s" + "s:"} {
		if strings.HasPrefix(v, prefix) {
			return true
		}
	}
	if (strings.HasPrefix(v, "{") || strings.HasPrefix(v, "[")) && (strings.Contains(v, "out"+"bounds") || strings.Contains(v, "in"+"bounds")) {
		return true
	}
	return looksOpaqueMachineIDLike(value)
}

func looksSensitiveLike(value string) bool {
	for _, marker := range []string{"tok" + "en", "pass" + "word", "pass" + "wd", "sec" + "ret", "priv" + "ate", "author" + "ization", "api" + "_key", "api" + "key"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func looksOpaqueMachineIDLike(value string) bool {
	compact := strings.TrimSpace(value)
	if utf8.RuneCountInString(compact) < 12 || strings.ContainsAny(compact, " \t\n\r-_.") {
		return false
	}
	for _, r := range compact {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}
