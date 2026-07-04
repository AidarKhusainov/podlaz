package cli

import (
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/AidarKhusainov/podlaz/internal/render"
)

type humanStatusMarks struct {
	OK      string
	Warn    string
	Blocked string
	Skip    string
}

func outputStatusMarks(plain bool) humanStatusMarks {
	if plain {
		return humanStatusMarks{OK: "OK", Warn: "WARN", Blocked: "BLOCKED", Skip: "SKIP"}
	}
	return humanStatusMarks{OK: "✓", Warn: "!", Blocked: "✗", Skip: "-"}
}

func renderAlignedField(w io.Writer, label, value string) {
	fmt.Fprintf(w, "  %-10s %s\n", label, render.Redact(strings.TrimSpace(value)))
}

func safeCommandProfileID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "<profile-id>"
	}
	if strings.ContainsAny(id, ".:/@") {
		return "<profile-id>"
	}
	return render.Redact(id)
}

func humanModeLabel(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "tun":
		return "TUN"
	case "proxy-only":
		return "Proxy-only"
	case "full-tunnel":
		return "Full tunnel"
	default:
		return humanTitle(mode)
	}
}

func humanBackendLabel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "xray":
		return "Xray"
	default:
		return humanTitle(value)
	}
}

func humanProtocolLabel(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func humanSourceLabel(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "_", "-")
	switch strings.ToLower(value) {
	case "manual":
		return "Manual"
	case "subscription":
		return "Subscription"
	case "imported-uri":
		return "Imported URI"
	default:
		return humanTitle(strings.ReplaceAll(value, "-", " "))
	}
}

func humanTitle(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	words := strings.Fields(value)
	for i, word := range words {
		runes := []rune(strings.ToLower(word))
		if len(runes) == 0 {
			continue
		}
		runes[0] = unicode.ToUpper(runes[0])
		words[i] = string(runes)
	}
	return strings.Join(words, " ")
}

func humanSingleLine(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}
