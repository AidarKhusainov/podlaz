package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
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
	completionDynamicNone          completionDynamicKind = ""
	completionDynamicProfiles      completionDynamicKind = "profiles"
	completionDynamicSubscriptions completionDynamicKind = "subscriptions"
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
		case completionDynamicProfiles:
			return noFileCompletion(profileCandidates(opts))
		case completionDynamicSubscriptions:
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
	jsonFlag := longBoolFlag("--json", "Print structured diagnostic output")
	yesFlag := longBoolFlag("--yes", "Confirm deletion without prompting")
	verboseFlag := completionFlag{Name: "--verbose", Shorthand: "-v", Description: "Show verbose diagnostic output", NonRepeatable: true}

	return &completionCommand{Children: []*completionCommand{
		{Name: "version", Description: "Show version"},
		{Name: "import", Description: "Import profile or subscription", DefaultFiles: true},
		{
			Name: "profile", Description: "Manage profiles", Children: []*completionCommand{
				{Name: "list", Description: "List profiles"},
				{Name: "show", Description: "Show profile", Dynamic: completionDynamicProfiles},
				{Name: "use", Description: "Select profile", Dynamic: completionDynamicProfiles},
				{Name: "delete", Description: "Delete profile", Flags: []completionFlag{yesFlag}, Dynamic: completionDynamicProfiles},
			},
		},
		{
			Name: "subscription", Description: "Manage subscriptions", Children: []*completionCommand{
				{Name: "list", Description: "List subscriptions"},
				{Name: "show", Description: "Show subscription", Dynamic: completionDynamicSubscriptions},
				{Name: "update", Description: "Update subscription", Dynamic: completionDynamicSubscriptions},
				{
					Name: "delete", Description: "Delete subscription",
					Flags: []completionFlag{yesFlag, longBoolFlag("--keep-profiles", "Keep imported profiles")},
					Dynamic: completionDynamicSubscriptions,
				},
			},
		},
		{Name: "connect", Description: "Connect full VPN", Dynamic: completionDynamicProfiles},
		{Name: "disconnect", Description: "Disconnect VPN"},
		{
			Name: "autostart", Description: "Manage boot autostart", Children: []*completionCommand{
				{Name: "enable", Description: "Enable selected VPN at boot", Dynamic: completionDynamicProfiles},
				{Name: "disable", Description: "Disable boot autostart"},
				{Name: "status", Description: "Show boot autostart"},
			},
		},
		{Name: "status", Description: "Show VPN status"},
		{
			Name: "debug", Description: "Advanced diagnostics", Children: []*completionCommand{
				{
					Name: "doctor", Description: "Run diagnostics",
					Flags: []completionFlag{
						longBoolFlag("--core", "Check core binary"),
						longBoolFlag("--tun", "Diagnose active TUN session"),
						longValueFlag("--xray", "Core binary path"),
						verboseFlag,
						jsonFlag,
					},
				},
				{
					Name: "logs", Description: "Show logs",
					Flags: []completionFlag{
						{Name: "--follow", Shorthand: "-f", Description: "Follow logs", NonRepeatable: true},
						longBoolFlag("--daemon", "Daemon logs"),
						longBoolFlag("--core", "Core logs"),
						longValueFlag("--since", "Duration <integer><s|m|h>, max 720h"),
					},
				},
				{Name: "proxy", Description: "Connect with Proxy-only protection", Dynamic: completionDynamicProfiles},
			},
		},
		{
			Name: "completion", Description: "Generate completion", Children: []*completionCommand{
				{Name: "bash", Description: "Bash script"},
				{Name: "zsh", Description: "Zsh script"},
				{Name: "fish", Description: "Fish script"},
			},
		},
		{
			Name: "help", Description: "Show help", Children: []*completionCommand{
				{Name: "version", Description: "Version help"},
				{Name: "import", Description: "Import help"},
				{Name: "profile", Description: "Profile help"},
				{Name: "subscription", Description: "Subscription help"},
				{Name: "connect", Description: "Connect help"},
				{Name: "disconnect", Description: "Disconnect help"},
				{Name: "autostart", Description: "Autostart help"},
				{Name: "status", Description: "Status help"},
				{Name: "debug", Description: "Advanced help"},
				{Name: "completion", Description: "Completion help"},
				{Name: "help", Description: "Help help"},
			},
		},
	}}
}

func longBoolFlag(name string, description string) completionFlag {
	return completionFlag{Name: name, Description: description, NonRepeatable: true}
}

func longValueFlag(name string, description string) completionFlag {
	return completionFlag{Name: name, Description: description, TakesValue: true, NonRepeatable: true}
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
