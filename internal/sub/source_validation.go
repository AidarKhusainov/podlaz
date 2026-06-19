package sub

import (
	"fmt"
	"net/url"
	"strings"
)

func ValidateSource(source Source) error {
	var messages []string
	if strings.TrimSpace(source.ID) == "" {
		messages = append(messages, "id is required")
	}
	if strings.TrimSpace(source.Name) == "" {
		messages = append(messages, "name is required")
	}
	if strings.TrimSpace(source.URL) == "" {
		messages = append(messages, "url is required")
	} else if err := validateSourceURL(source.URL); err != nil {
		messages = append(messages, err.Error())
	}
	switch source.Format {
	case FormatBase64, FormatXrayJSON:
	case "":
		messages = append(messages, "format is required")
	default:
		messages = append(messages, fmt.Sprintf("unsupported subscription format %q", source.Format))
	}
	if len(messages) > 0 {
		return fmt.Errorf("invalid subscription: %s", strings.Join(messages, "; "))
	}
	return nil
}

func validateSourceURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("url is invalid: %w", err)
	}
	if u.Scheme == "" {
		return fmt.Errorf("url must include a scheme")
	}
	switch strings.ToLower(u.Scheme) {
	case "file":
		if u.Host != "" && u.Host != "localhost" {
			return fmt.Errorf("file URL host must be empty or localhost")
		}
		if u.Path == "" {
			return fmt.Errorf("file URL path is required")
		}
	case "http", "https":
		if u.Host == "" {
			return fmt.Errorf("url host is required")
		}
	default:
		return fmt.Errorf("unsupported url scheme %q", u.Scheme)
	}
	return nil
}
