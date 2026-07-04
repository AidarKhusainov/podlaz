package render

import (
	"regexp"
	"strings"
)

var (
	urlQueryPattern         = regexp.MustCompile(`https?://[^\s?]+\?[^\s]+`)
	secretAssignmentPattern = regexp.MustCompile(`(?i)\b(token|password|passwd|secret|api[_-]?key|authorization)=([^\s;]+)`)
	secretValuePattern      = regexp.MustCompile(`(?i)(^|[:/._-])(password|passwd|secret)([:/._-]|$)`)
	uuidPattern             = regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`)
)

// Redact returns a single-line string safe for default human CLI output.
func Redact(s string) string {
	trimmed := strings.TrimSpace(s)
	if looksLikeStructuredPayload(trimmed) {
		return "REDACTED"
	}
	s = strings.Join(strings.Fields(s), " ")
	if secretValuePattern.MatchString(s) {
		return "REDACTED"
	}
	s = urlQueryPattern.ReplaceAllStringFunc(s, func(match string) string {
		idx := strings.Index(match, "?")
		if idx < 0 {
			return match
		}
		return match[:idx] + "?REDACTED"
	})
	s = secretAssignmentPattern.ReplaceAllString(s, "$1=REDACTED")
	s = uuidPattern.ReplaceAllStringFunc(s, func(match string) string {
		return match[:4] + "…" + match[len(match)-4:]
	})
	return s
}

func looksLikeStructuredPayload(s string) bool {
	if len(s) < 2 {
		return false
	}
	return (strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}")) ||
		(strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]"))
}
