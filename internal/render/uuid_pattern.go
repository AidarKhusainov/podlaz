package render

import (
	"regexp"
	"strings"
)

func init() {
	parts := []string{
		`\b`,
		`[0-9a-fA-F]{8}`,
		`-`,
		`[0-9a-fA-F]{4}`,
		`-`,
		`[0-9a-fA-F]{4}`,
		`-`,
		`[0-9a-fA-F]{4}`,
		`-`,
		`[0-9a-fA-F]{12}`,
		`\b`,
	}
	uuidPattern = regexp.MustCompile(strings.Join(parts, ""))
}
