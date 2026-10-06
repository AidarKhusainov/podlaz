package cli

import (
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/render"
	"github.com/AidarKhusainov/podlaz/internal/sub"
)

func mustCompletionCommand(path ...string) *completionCommand {
	node := completionRegistry()
	for _, name := range path {
		node = node.child(name)
		if node == nil {
			panic("missing completion command metadata: " + strings.Join(path, " "))
		}
	}
	return node
}

func childNames(command *completionCommand) []string {
	names := make([]string, 0, len(command.Children))
	for _, child := range command.Children {
		names = append(names, child.Name)
	}
	return names
}

func (c *completionCommand) child(name string) *completionCommand {
	for _, child := range c.Children {
		if child.Name == name {
			return child
		}
	}
	return nil
}

func (c *completionCommand) findFlag(name string) (completionFlag, bool) {
	for _, flag := range c.Flags {
		if flag.Name == name || flag.Shorthand == name {
			return flag, true
		}
	}
	return completionFlag{}, false
}

func (f completionFlag) canonicalName() string {
	if f.Name != "" {
		return f.Name
	}
	return f.Shorthand
}

func completionWordAt(words []string, cursor int) string {
	if cursor < len(words) {
		return words[cursor]
	}
	return ""
}

func noFileCompletion(candidates []completionCandidate) completionResult {
	return completionResult{Candidates: candidates, Directives: []completionDirective{completionDirectiveNoFiles}}
}

func commandCandidates(commands []*completionCommand) []completionCandidate {
	candidates := make([]completionCandidate, 0, len(commands))
	for _, command := range commands {
		candidates = append(candidates, completionCandidate{Value: command.Name, Description: command.Description})
	}
	return candidates
}

func flagCandidates(flags []completionFlag, used map[string]struct{}) []completionCandidate {
	var candidates []completionCandidate
	for _, flag := range flags {
		if flag.NonRepeatable {
			if _, ok := used[flag.canonicalName()]; ok {
				continue
			}
		}
		if flag.Name != "" {
			candidates = append(candidates, completionCandidate{Value: flag.Name, Description: flag.Description})
		}
		if flag.Shorthand != "" {
			candidates = append(candidates, completionCandidate{Value: flag.Shorthand, Description: flag.Description})
		}
	}
	return candidates
}

func valueCandidates(values []string, prefix string) []completionCandidate {
	candidates := make([]completionCandidate, 0, len(values))
	for _, value := range values {
		candidates = append(candidates, completionCandidate{Value: prefix + value})
	}
	return candidates
}

func profileCandidates(opts options) []completionCandidate {
	store, err := profile.NewStore(opts.profileStorePath)
	if err != nil {
		return nil
	}
	profiles, err := store.List()
	if err != nil {
		return nil
	}
	selectedID, _ := store.SelectedID()

	nameCounts := make(map[string]int, len(profiles))
	for _, p := range profiles {
		nameCounts[strings.ToLower(strings.TrimSpace(p.Name))]++
	}

	candidates := make([]completionCandidate, 0, len(profiles))
	for _, p := range profiles {
		key := strings.ToLower(strings.TrimSpace(p.Name))
		value := render.Redact(p.Name)
		description := render.Redact(p.Protocol)
		if nameCounts[key] > 1 {
			value = render.Redact(p.ID)
			description = render.Redact(p.Name) + " · stable ID"
		}
		if p.ID == selectedID {
			if description != "" {
				description += " · selected"
			} else {
				description = "selected"
			}
		}
		candidates = append(candidates, completionCandidate{Value: value, Description: description})
	}
	return candidates
}

func subscriptionIDCandidates(opts options) []completionCandidate {
	storePath, err := resolvedSubscriptionStorePath(opts)
	if err != nil {
		return nil
	}
	store, err := sub.NewStore(storePath)
	if err != nil {
		return nil
	}
	sources, err := store.List()
	if err != nil {
		return nil
	}
	candidates := make([]completionCandidate, 0, len(sources))
	for _, source := range sources {
		candidates = append(candidates, completionCandidate{Value: render.Redact(source.ID), Description: render.Redact(source.Name)})
	}
	return candidates
}

func splitFlagToken(word string) (string, string, bool) {
	name, value, ok := strings.Cut(word, "=")
	return name, value, ok
}

func inlineFlagValue(word string) (string, string, bool) {
	if !strings.HasPrefix(word, "--") {
		return "", "", false
	}
	return splitFlagToken(word)
}
