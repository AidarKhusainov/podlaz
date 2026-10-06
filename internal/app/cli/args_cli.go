package cli

import "strings"

func cutFlagValue(arg string) (string, bool) {
	_, value, ok := strings.Cut(arg, "=")
	return value, ok
}

func flagValue(flag string, args []string, index int, inlineValue string, hasInlineValue bool) (string, int, error) {
	if hasInlineValue {
		if strings.TrimSpace(inlineValue) == "" {
			return "", index, usageError("%s requires a value", flag)
		}
		return inlineValue, index, nil
	}
	if index+1 >= len(args) || strings.TrimSpace(args[index+1]) == "" || strings.HasPrefix(args[index+1], "--") {
		return "", index, usageError("%s requires a value", flag)
	}
	return args[index+1], index + 1, nil
}
