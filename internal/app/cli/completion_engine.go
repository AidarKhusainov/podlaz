package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/AidarKhusainov/tunwarden/internal/network/planner"
	"github.com/AidarKhusainov/tunwarden/internal/profile"
	"github.com/AidarKhusainov/tunwarden/internal/render"
	"github.com/AidarKhusainov/tunwarden/internal/sub"
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
	TakesValue    bool
	Values        []string
	NonRepeatable bool
}

type completionCommand struct {
	Name         string
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
		words = []string{"tunwarden"}
	}

	result := completeTunWarden(completionRequest{Shell: strings.ToLower(args[0]), Cursor: cursor, Words: words}, opts)
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

func completeTunWarden(req completionRequest, opts options) completionResult {
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
	return &completionCommand{Children: []*completionCommand{
		{Name: "version"},
		{Name: "import", DefaultFiles: true},
		{Name: "profile", Children: []*completionCommand{
			{Name: "add", Flags: []completionFlag{
				longValueFlag("--name"),
				longValueFlag("--server"),
				longValueFlag("--port"),
				longEnumFlag("--protocol", protocols),
			}},
			{Name: "import"},
			{Name: "list", Flags: []completionFlag{longBoolFlag("--json")}},
			{Name: "show", Flags: []completionFlag{longBoolFlag("--json")}, Dynamic: completionDynamicProfileIDs},
			{Name: "validate", Flags: []completionFlag{longEnumFlag("--mode", modes), longBoolFlag("--json")}, Dynamic: completionDynamicProfileIDs},
			{Name: "delete", Flags: []completionFlag{longBoolFlag("--yes")}, Dynamic: completionDynamicProfileIDs},
		}},
		{Name: "subscription", Children: []*completionCommand{
			{Name: "add", Flags: []completionFlag{longValueFlag("--name"), longValueFlag("--url")}},
			{Name: "list", Flags: []completionFlag{longBoolFlag("--json")}},
			{Name: "show", Flags: []completionFlag{longBoolFlag("--json")}, Dynamic: completionDynamicSubscriptionIDs},
			{Name: "update", Dynamic: completionDynamicSubscriptionIDs},
			{Name: "delete", Flags: []completionFlag{longBoolFlag("--yes"), longBoolFlag("--keep-profiles")}, Dynamic: completionDynamicSubscriptionIDs},
		}},
		{Name: "plan", Flags: []completionFlag{longEnumFlag("--mode", modes), longBoolFlag("--json")}, Dynamic: completionDynamicProfileIDs},
		{Name: "connect", Flags: []completionFlag{longEnumFlag("--mode", modes)}, Dynamic: completionDynamicProfileIDs},
		{Name: "disconnect"},
		{Name: "status"},
		{Name: "doctor", Flags: []completionFlag{longBoolFlag("--core"), longValueFlag("--xray"), longBoolFlag("--json")}},
		{Name: "logs", Flags: []completionFlag{
			{Name: "--follow", Shorthand: "-f", NonRepeatable: true},
			longBoolFlag("--daemon"),
			longBoolFlag("--core"),
			longValueFlag("--since"),
		}},
		{Name: "recover", Flags: []completionFlag{longBoolFlag("--execute"), longBoolFlag("--yes"), longBoolFlag("--json")}},
		{Name: "completion", Children: []*completionCommand{{Name: "bash"}, {Name: "zsh"}, {Name: "fish"}}},
		{Name: "help", Children: []*completionCommand{
			{Name: "version"}, {Name: "import"}, {Name: "profile"}, {Name: "subscription"}, {Name: "plan"}, {Name: "connect"}, {Name: "disconnect"}, {Name: "status"}, {Name: "doctor"}, {Name: "logs"}, {Name: "recover"}, {Name: "completion"}, {Name: "help"},
		}},
	}}
}

func longBoolFlag(name string) completionFlag {
	return completionFlag{Name: name, NonRepeatable: true}
}

func longValueFlag(name string) completionFlag {
	return completionFlag{Name: name, TakesValue: true, NonRepeatable: true}
}

func longEnumFlag(name string, values []string) completionFlag {
	return completionFlag{Name: name, TakesValue: true, Values: values, NonRepeatable: true}
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

func completionProfileProtocolNames() []string {
	flag, _ := mustCompletionCommand("profile", "add").findFlag("--protocol")
	return append([]string(nil), flag.Values...)
}
