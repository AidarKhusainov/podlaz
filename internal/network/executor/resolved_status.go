package executor

import (
	"fmt"
	"strings"
)

func validateObservedCommandResult(name string, args []string, result CommandResult) error {
	if name != "resolvectl" || !equalCommandArgs(args, []string{"status", "--no-pager"}) {
		return nil
	}
	if duplicate := duplicateResolvedLinkSectionName(result.Stdout); duplicate != "" {
		return fmt.Errorf("duplicate systemd-resolved link section %q", duplicate)
	}
	return nil
}

func duplicateResolvedLinkSectionName(output string) string {
	seen := make(map[string]struct{})
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Link ") {
			continue
		}
		open := strings.LastIndex(line, "(")
		close := strings.LastIndex(line, ")")
		if open < 0 || close <= open {
			continue
		}
		name := strings.TrimSpace(line[open+1 : close])
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			return name
		}
		seen[name] = struct{}{}
	}
	return ""
}

func equalCommandArgs(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
