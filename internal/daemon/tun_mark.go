package daemon

import (
	"strconv"
	"strings"
)

func canonicalTunFwmarkIdentity(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", true
	}
	parts := strings.SplitN(value, "/", 2)
	mark, err := strconv.ParseUint(parts[0], 0, 32)
	if err != nil {
		return "", false
	}
	mask := uint64(^uint32(0))
	if len(parts) == 2 {
		mask, err = strconv.ParseUint(parts[1], 0, 32)
		if err != nil {
			return "", false
		}
	}
	return strconv.FormatUint(mark, 10) + "/" + strconv.FormatUint(mask, 10), true
}

func normalizedTunFwmarkIdentity(value string) string {
	canonical, ok := canonicalTunFwmarkIdentity(value)
	if ok {
		return canonical
	}
	return strings.TrimSpace(value)
}
