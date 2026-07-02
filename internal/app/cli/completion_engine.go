package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	profilecheck "github.com/AidarKhusainov/podlaz/internal/check"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/render"
	"github.com/AidarKhusainov/podlaz/internal/sub"
)

type completionDirective string

const (
	completionDirectiveNoFiles      completionDirective = "no-files"
	completionDirectiveDefaultFiles completionDirective = "default-files"
	completionDirectiveNoSpace      completionDirective = "no-space"
)

type completionCandidate struct {
	Value       string
	Description string
}

type completionResult struct {
	Candidates []completionCandidate
	Directives []completionDirective
}

type completionRequest struct {
	Shell  string
	Cursor int
	Words  []string
}

type completionDynamicKind string

const (
	completionDynamicNone            completionDynamicKind = ""
	completionDynamicProfileIDs      completionDynamicKind = "profile-ids"
	completionDynamicSubscriptionIDs completionDynamicKind = "subscription-ids"
)

type completionFlag struct {
	Name          string
	Shorthand     string
	Description   string
	TakesValue    bool
	Values        []string
	NonRepeatable bool
}

type completionCommand struct {
	Name         string
	Description  string
	Children     []*completionCommand
	Flags        []completionFlag
	Dynamic      completionDynamicKind
	DefaultFiles bool
}

type completionAnalysis struct {
	Node        *completionCommand
	UsedFlags   map[string]struct{}
	Positionals []string
	ValueFlag   *completionFlag
}

func runCompletionRuntimeCommand(args []string, stdout io.Writer, opts options) error {
	if len(args) < 2 {
		return usageError("__complete requires shell, cursor index, and words")
	}
	cursor, err := strconv.Atoi(args[1])
	if err != nil || cursor < 0 {
		return usageError("__complete cursor index must be a non-negative integer")
	}
	words := args[2:]
	if len(words) == 0 {
		words = []string{"podlaz"}
	}

	result := completepodlaz(completionRequest{Shell: strings.ToLower(args[0]), Cursor: cursor, Words: words}, opts)
	for _, directive := range result.Directives {
		fmt.Fprintf(stdout, ":%s\n", directive)
	}
	for _, candidate := range result.Candidates {
		if candidate.Description == "" {
			fmt.Fprintln(stdout, candidate.Value)
			continue
		}
		fmt.Fprintf(stdout, "%s\t%s\n", candidate.Value, candidate.Description)
	}
	return nil
}

func completepodlaz(req completionRequest, opts options) completionResult {
	switch req.Shell {
	case "", "bash", "zsh", "fish":
	default:
		return noFileCompletion(nil)
	}

	registry := completionRegistry()
	if req.Cursor <= 0 {
		return noFileCompletion(commandCandidates(registry.Children))
	}
	if req.Cursor > len(req.Words) {
		req.Cursor = len(req.Words)
	}

	current := completionWordAt(req.Words, req.Cursor)
	analysis := analyzeCompletion(registry, req.Words, req.Cursor)
	if analysis.ValueFlag != nil {
		return noFileCompletion(valueCandidates(analysis.ValueFlag.Values, ""))
	}
	if flagName, _, ok := inlineFlagValue(current); ok {
		if flag, found := analysis.Node.findFlag(flagName); found && flag.TakesValue {
			return noFileCompletion(valueCandidates(flag.Values, flagName+"="))
		}
	}
	if strings.HasPrefix(current, "-") {
		return noFileCompletion(flagCandidates(analysis.Node.Flags, analysis.UsedFlags))
	}
	if len(analysis.Positionals) == 0 {
		if len(analysis.Node.Children) > 0 {
			return noFileCompletion(commandCandidates(analysis.Node.Children))
		}
		switch analysis.Node.Dynamic {
		case completionDynamicProfileIDs:
			return noFileCompletion(profileIDCandidates(opts))
		case completionDynamicSubscriptionIDs:
			return noFileCompletion(subscriptionIDCandidates(opts))
		}
	}
	if analysis.Node.DefaultFiles {
		return completionResult{Directives: []completionDirective{completionDirectiveDefaultFiles}}
	}
	return noFileCompletion(nil)
}

func analyzeCompletion(root *completionCommand, words []string, cursor int) completionAnalysis {
	analysis := completionAnalysis{Node: root, UsedFlags: map[string]struct{}{}}
	for i := 1; i < cursor && i < len(words); i++ {
		word := words[i]
		if word == "" {
			continue
		}
		if strings.HasPrefix(word, "-") {
			flagName, _, hasInlineValue := splitFlagToken(word)
			flag, ok := analysis.Node.findFlag(flagName)
			if !ok {
				continue
			}
			analysis.UsedFlags[flag.canonicalName()] = struct{}{}
			if flag.TakesValue && !hasInlineValue {
				if i == cursor-1 {
					copyFlag := flag
					analysis.ValueFlag = &copyFlag
					break
				}
				if i+1 < cursor {
					i++
				}
			}
			continue
		}
		if len(analysis.Positionals) == 0 {
			if child := analysis.Node.child(word); child != nil {
				analysis.Node = child
				continue
			}
		}
		analysis.Positionals = append(analysis.Positionals, word)
	}
	return analysis
}

func completionRegistry() *completionCommand {
	modes := []string{planner.ModeProxyOnly, planner.ModeTun}
	protocols := []string{"vless", "vmess", "trojan", "shadowsocks"}
	targetIDs := profilecheck.SupportedTargetIDs()
	jsonFlag := longBoolFlag("--json", "Print JSON output")
	yesFlag := longBoolFlag("--yes", "Confirm without prompting")
	modeFlag := longEnumFlag("--mode", modes, "Select connection mode")
	targetFlag := completionFlag{Name: "--target", Description: "Select service target", TakesValue: true, Values: targetIDs}
	return &completionCommand{Children: []*completionCommand{
		{Name: "version", Description: "Show version"},
		{Name: "import", Description: "Import profile or subscription", DefaultFiles: true},
		{Name: "profile", Description: "Manage profiles", Children: []*completionCommand{
			{Name: "add", Description: "Add manual profile", Flags: []completionFlag{
				longValueFlag("--name", "Profile name"),
				longValueFlag("--server", "Server hostname"),
				longValueFlag("--port", "Server port"),
				longEnumFlag("--protocol", protocols, "Profile protocol"),
			}},
			{Name: "import", Description: "Import share URI"},
			{Name: "list", Description: "List profiles", Flags: []completionFlag{jsonFlag}},
			{Name: "show", Description: "Show profile", Flags: []completionFlag{jsonFlag}, Dynamic: completionDynamicProfileIDs},
			{Name: "validate", Description: "Validate profile", Flags: []completionFlag{modeFlag, jsonFlag}, Dynamic: completionDynamicProfileIDs},
			{Name: "delete", Description: "Delete profile", Flags: []completionFlag{yesFlag}, Dynamic: completionDynamicProfileIDs},
		}},
		{Name: "subscription", Description: "Manage subscriptions", Children: []*completionCommand{
			{Name: "add", Description: "Add subscription", Flags: []completionFlag{longValueFlag("--name", "Subscription name"), longValueFlag("--url", "Subscription URL")}},
			{Name: "list", Description: "List subscriptions", Flags: []completionFlag{jsonFlag}},
			{Name: "show", Description: "Show subscription", Flags: []completionFlag{jsonFlag}, Dynamic: completionDynamicSubscriptionIDs},
			{Name: "update", Description: "Fetch subscription", Dynamic: completionDynamicSubscriptionIDs},
			{Name: "delete", Description: "Delete subscription", Flags: []completionFlag{yesFlag, longBoolFlag("--keep-profiles", "Keep imported profiles")}, Dynamic: completionDynamicSubscriptionIDs},
		}},
		{Name: "plan", Description: "Preview connection plan", Flags: []completionFlag{modeFlag, jsonFlag}, Dynamic: completionDynamicProfileIDs},
		{Name: "connect", Description: "Start connection", Flags: []completionFlag{modeFlag}, Dynamic: completionDynamicProfileIDs},
		{Name: "disconnect", Description: "Stop connection"},
		{Name: "check", Description: "Check profile connectivity", Flags: []completionFlag{longBoolFlag("--all", "Check all profiles"), targetFlag, longValueFlag("--timeout", "Per-probe timeout"), jsonFlag}, Dynamic: completionDynamicProfileIDs},
		{Name: "status", Description: "Show status"},
		{Name: "doctor", Description: "Run diagnostics", Flags: []completionFlag{longBoolFlag("--core", "Check core binary"), longValueFlag("--xray", "Core binary path"), jsonFlag}},
		{Name: "logs", Description: "Show logs", Flags: []completionFlag{
			{Name: "--follow", Shorthand: "-f", Description: "Follow logs", NonRepeatable: true},
			longBoolFlag("--daemon", "Daemon logs"),
			longBoolFlag("--core", "Core logs"),
			longValueFlag("--since", "Journal time filter"),
		}},
		{Name: "recover", Description: "Inspect recovery", Flags: []completionFlag{longBoolFlag("--execute", "Execute cleanup"), yesFlag, jsonFlag}},
		{Name: "completion", Description: "Generate completion", Children: []*completionCommand{{Name: "bash", Description: "Bash script"}, {Name: "zsh", Description: "Zsh script"}, {Name: "fish", Description: "Fish script"}}},
		{Name: "help", Description: "Show help", Children: []*completionCommand{
			{Name: "version", Description: "Version help"}, {Name: "import", Description: "Import help"}, {Name: "profile", Description: "Profile help"}, {Name: "subscription", Description: "Subscription help"}, {Name: "plan", Description: "Plan help"}, {Name: "connect", Description: "Connect help"}, {Name: "disconnect", Description: "Disconnect help"}, {Name: "check", Description: "Check help"}, {Name: "status", Description: "Status help"}, {Name: "doctor", Description: "Doctor help"}, {Name: "logs", Description: "Logs help"}, {Name: "recover", Description: "Recover help"}, {Name: "completion", Description: "Completion help"}, {Name: "help", Description: "Help help"},
		}},
	}}
}

func longBoolFlag(name string, description string) completionFlag {
	return completionFlag{Name: name, Description: description, NonRepeatable: true}
}

func longValueFlag(name string, description string) completionFlag {
	return completionFlag{Name: name, Description: description, TakesValue: true, NonRepeatable: true}
}

func longEnumFlag(name string, values []string, description string) completionFlag {
	return completionFlag{Name: name, Description: description, TakesValue: true, Values: values, NonRepeatable: true}
}

func completionTopLevelCommandNames() []string {
	return childNames(completionRegistry())
}

func completionProfileCommandNames() []string {
	return childNames(mustCompletionCommand("profile"))
}

func completionSubscriptionCommandNames() []string {
	return childNames(mustCompletionCommand("subscription"))
}

func completionShellNames() []string {
	return childNames(mustCompletionCommand("completion"))
}

func completionConnectionModeNames() []string {
	flag, _ := mustCompletionCommand("plan").findFlag("--mode")
	return append([]string(nil), flag.Values...)
}
