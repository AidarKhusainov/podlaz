package snapshot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

func ipv4PolicyRules(ctx context.Context, runner CommandRunner, ipPath string) PolicyRuleInventory {
	result, err := runCommand(ctx, runner, ipPath, "-4", "rule", "show")
	if !commandSucceeded(result, err) {
		return PolicyRuleInventory{Inspection: findingWithDetail(StatusUnknown, "IPv4 policy-rule inventory unavailable", commandFailureMessage(result, err))}
	}
	rules, parseErr := ParseIPv4PolicyRules(result.Stdout)
	if parseErr != nil {
		return PolicyRuleInventory{Inspection: findingWithDetail(StatusUnknown, "IPv4 policy-rule inventory malformed", parseErr.Error())}
	}
	return PolicyRuleInventory{Inspection: finding(StatusDetected, "IPv4 policy-rule inventory available"), Rules: rules}
}

// ParseIPv4PolicyRules parses the complete IPv4 policy-rule inventory needed
// for collision-free session allocation. Historical Podlaz-looking priorities
// and table IDs are deliberately not filtered: numeric resemblance is not
// ownership evidence and still means the resource is occupied.
func ParseIPv4PolicyRules(output string) ([]PolicyRoutingSignal, error) {
	var out []PolicyRoutingSignal
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		priorityText := strings.TrimSuffix(fields[0], ":")
		priority, err := strconv.ParseUint(priorityText, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid IPv4 policy-rule priority")
		}

		sig := PolicyRoutingSignal{Kind: "rule", Priority: priorityText, Raw: line}
		var from, to string
		for i := 1; i+1 < len(fields); i++ {
			switch fields[i] {
			case "lookup", "table":
				sig.Table = fields[i+1]
			case "fwmark":
				sig.Fwmark = fields[i+1]
			case "from":
				from = fields[i+1]
			case "to":
				to = fields[i+1]
			}
		}
		sig.Selector = policyRuleSelector(from, to)
		if defaultKernelPolicyRule(uint32(priority), sig.Table) {
			continue
		}
		out = append(out, sig)
	}
	return out, nil
}

func policyRuleSelector(from, to string) string {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if to != "" {
		if from != "" && from != "all" {
			return "from " + from + " to " + to
		}
		return "to " + to
	}
	if from != "" {
		return "from " + from
	}
	return ""
}

func defaultKernelPolicyRule(priority uint32, table string) bool {
	switch priority {
	case 0:
		return table == "local"
	case 32766:
		return table == "main"
	case 32767:
		return table == "default"
	default:
		return false
	}
}
