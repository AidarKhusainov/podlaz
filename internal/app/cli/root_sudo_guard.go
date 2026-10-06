package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

var (
	currentEffectiveUID = os.Geteuid
	currentSudoUser     = func() string { return os.Getenv("SUDO_USER") }
)

func guardSudoUserStateCommand(command string, args []string) error {
	if !isSudoRootInvocation() || !commandUsesUserOwnedState(command, args) {
		return nil
	}
	return sudoUserOwnedStateError(sudoGuardCommandShape(command, args))
}

func isSudoRootInvocation() bool {
	return currentEffectiveUID() == 0 && strings.TrimSpace(currentSudoUser()) != ""
}

func commandUsesUserOwnedState(command string, args []string) bool {
	command = strings.ToLower(command)
	if command == "__complete" {
		return completionRuntimeUsesUserOwnedState(args)
	}
	if isHelp(args) {
		return false
	}
	switch command {
	case "import", "profile", "subscription", "connect":
		return true
	case "autostart":
		return len(args) > 0 && strings.EqualFold(args[0], "enable")
	case "debug":
		return len(args) > 0 && strings.EqualFold(args[0], "proxy")
	default:
		return false
	}
}

func completionRuntimeUsesUserOwnedState(args []string) bool {
	req, ok := sudoGuardCompletionRequest(args)
	if !ok {
		return false
	}
	if req.Cursor <= 0 {
		return false
	}
	if req.Cursor > len(req.Words) {
		req.Cursor = len(req.Words)
	}

	current := completionWordAt(req.Words, req.Cursor)
	analysis := analyzeCompletion(completionRegistry(), req.Words, req.Cursor)
	if analysis.ValueFlag != nil {
		return false
	}
	if flagName, _, ok := inlineFlagValue(current); ok {
		if flag, found := analysis.Node.findFlag(flagName); found && flag.TakesValue {
			return false
		}
	}
	if strings.HasPrefix(current, "-") {
		return false
	}
	return len(analysis.Positionals) == 0 && analysis.Node.Dynamic != completionDynamicNone
}

func sudoGuardCompletionRequest(args []string) (completionRequest, bool) {
	if len(args) < 2 {
		return completionRequest{}, false
	}
	cursor, err := strconv.Atoi(args[1])
	if err != nil || cursor < 0 {
		return completionRequest{}, false
	}
	shell := strings.ToLower(args[0])
	switch shell {
	case "", "bash", "zsh", "fish":
	default:
		return completionRequest{}, false
	}
	words := args[2:]
	if len(words) == 0 {
		words = []string{"podlaz"}
	}
	return completionRequest{Shell: shell, Cursor: cursor, Words: words}, true
}

func sudoUserOwnedStateError(shape string) error {
	return exitError{code: 4, err: fmt.Errorf("this command uses user-owned state and must not be run with sudo.\nRun it as your user:\n\n  %s", shape)}
}

func sudoGuardCommandShape(command string, args []string) string {
	switch strings.ToLower(command) {
	case "__complete":
		if req, ok := sudoGuardCompletionRequest(args); ok && len(req.Words) > 1 {
			return sudoGuardCommandShape(req.Words[1], completionCommandArgs(req.Words))
		}
		return "podlaz <command>"
	case "import":
		return "podlaz import <uri|url|file>"
	case "profile":
		return sudoGuardProfileCommandShape(args)
	case "subscription":
		return sudoGuardSubscriptionCommandShape(args)
	case "connect":
		return "podlaz connect [profile]"
	case "autostart":
		return "podlaz autostart enable [profile]"
	case "debug":
		return "podlaz debug proxy <profile>"
	default:
		return "podlaz <command>"
	}
}

func completionCommandArgs(words []string) []string {
	if len(words) <= 2 {
		return nil
	}
	return words[2:]
}

func sudoGuardProfileCommandShape(args []string) string {
	if len(args) == 0 {
		return "podlaz profile <list|show|use|delete>"
	}
	switch strings.ToLower(args[0]) {
	case "list":
		return "podlaz profile list"
	case "show":
		return "podlaz profile show <profile>"
	case "use":
		return "podlaz profile use <profile>"
	case "delete":
		return "podlaz profile delete <profile> --yes"
	default:
		return "podlaz profile <subcommand>"
	}
}

func sudoGuardSubscriptionCommandShape(args []string) string {
	if len(args) == 0 {
		return "podlaz subscription <list|show|update|delete>"
	}
	switch strings.ToLower(args[0]) {
	case "list":
		return "podlaz subscription list"
	case "show":
		return "podlaz subscription show <subscription-id>"
	case "update":
		return "podlaz subscription update <subscription-id>"
	case "delete":
		return "podlaz subscription delete <subscription-id> --yes"
	default:
		return "podlaz subscription <subcommand>"
	}
}
