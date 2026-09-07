package executor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

const supportedNftJSONSchemaVersion = 1

type observedNftJSONChain struct {
	name     string
	typeName string
	hook     string
	priority int
	policy   string
	rules    [][]string
}

type nftJSONDocument struct {
	Nftables []map[string]json.RawMessage `json:"nftables"`
}

type nftJSONMetaInfo struct {
	Version           string `json:"version"`
	ReleaseName       string `json:"release_name"`
	JSONSchemaVersion int    `json:"json_schema_version"`
}

type nftJSONTable struct {
	Family string `json:"family"`
	Name   string `json:"name"`
	Handle uint64 `json:"handle,omitempty"`
}

type nftJSONChain struct {
	Family string      `json:"family"`
	Table  string      `json:"table"`
	Name   string      `json:"name"`
	Handle uint64      `json:"handle,omitempty"`
	Type   string      `json:"type"`
	Hook   string      `json:"hook"`
	Prio   json.Number `json:"prio"`
	Policy string      `json:"policy"`
}

type nftJSONRule struct {
	Family  string            `json:"family"`
	Table   string            `json:"table"`
	Chain   string            `json:"chain"`
	Expr    []json.RawMessage `json:"expr"`
	Handle  uint64            `json:"handle,omitempty"`
	Index   uint64            `json:"index,omitempty"`
	Comment string            `json:"comment,omitempty"`
}

func parseOwnedNftJSONTable(output, family, table string) ([]observedNftJSONChain, error) {
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.UseNumber()
	var doc nftJSONDocument
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("invalid nftables JSON: %w", err)
	}
	if decoder.More() {
		return nil, errors.New("nftables JSON contains trailing data")
	}
	if len(doc.Nftables) == 0 {
		return nil, errors.New("nftables JSON is empty")
	}

	seenMeta := false
	seenTable := false
	chains := make([]observedNftJSONChain, 0, 1)
	chainIndex := map[string]int{}
	for objectIndex, object := range doc.Nftables {
		if len(object) != 1 {
			return nil, fmt.Errorf("nftables JSON object[%d] has %d members, want 1", objectIndex, len(object))
		}
		for kind, raw := range object {
			switch kind {
			case "metainfo":
				if seenMeta || objectIndex != 0 {
					return nil, errors.New("nftables JSON metainfo is duplicated or out of order")
				}
				var meta nftJSONMetaInfo
				if err := decodeStrictNftJSON(raw, &meta); err != nil {
					return nil, fmt.Errorf("invalid nftables JSON metainfo: %w", err)
				}
				if meta.JSONSchemaVersion != supportedNftJSONSchemaVersion {
					return nil, fmt.Errorf("unsupported nftables JSON schema version %d", meta.JSONSchemaVersion)
				}
				seenMeta = true
			case "table":
				if seenTable {
					return nil, errors.New("nftables JSON contains multiple table objects")
				}
				var observed nftJSONTable
				if err := decodeStrictNftJSON(raw, &observed); err != nil {
					return nil, fmt.Errorf("invalid nftables table object: %w", err)
				}
				if observed.Family != family || observed.Name != table {
					return nil, fmt.Errorf("unexpected nftables table identity %s %s", observed.Family, observed.Name)
				}
				seenTable = true
			case "chain":
				if !seenTable {
					return nil, errors.New("nftables chain appears before target table")
				}
				var observed nftJSONChain
				if err := decodeStrictNftJSON(raw, &observed); err != nil {
					return nil, fmt.Errorf("invalid nftables chain object: %w", err)
				}
				if observed.Family != family || observed.Table != table || strings.TrimSpace(observed.Name) == "" {
					return nil, fmt.Errorf("unexpected nftables chain identity %s %s %s", observed.Family, observed.Table, observed.Name)
				}
				if _, duplicate := chainIndex[observed.Name]; duplicate {
					return nil, fmt.Errorf("duplicate nftables chain %q", observed.Name)
				}
				priority, err := parseNftJSONPriority(observed.Prio)
				if err != nil {
					return nil, fmt.Errorf("nftables chain %s: %w", observed.Name, err)
				}
				chainIndex[observed.Name] = len(chains)
				chains = append(chains, observedNftJSONChain{
					name: observed.Name, typeName: observed.Type, hook: observed.Hook,
					priority: priority, policy: observed.Policy,
				})
			case "rule":
				var observed nftJSONRule
				if err := decodeStrictNftJSON(raw, &observed); err != nil {
					return nil, fmt.Errorf("invalid nftables rule object: %w", err)
				}
				if observed.Family != family || observed.Table != table {
					return nil, fmt.Errorf("unexpected nftables rule identity %s %s", observed.Family, observed.Table)
				}
				index, exists := chainIndex[observed.Chain]
				if !exists {
					return nil, fmt.Errorf("nftables rule references unknown chain %q", observed.Chain)
				}
				rule, err := canonicalObservedNftJSONRule(observed)
				if err != nil {
					return nil, fmt.Errorf("nftables chain %s rule[%d]: %w", observed.Chain, len(chains[index].rules), err)
				}
				chains[index].rules = append(chains[index].rules, rule)
			default:
				return nil, fmt.Errorf("unexpected nftables JSON object %q in owned table", kind)
			}
		}
	}
	if !seenMeta {
		return nil, errors.New("nftables JSON metainfo is missing")
	}
	if !seenTable {
		return nil, errors.New("nftables JSON target table is missing")
	}
	return chains, nil
}

func decodeStrictNftJSON(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("trailing JSON data")
	}
	return nil
}

func parseNftJSONPriority(value json.Number) (int, error) {
	if value == "" {
		return 0, errors.New("missing numeric base-chain priority")
	}
	priority, err := strconv.Atoi(value.String())
	if err != nil {
		return 0, fmt.Errorf("non-numeric base-chain priority %q", value)
	}
	return priority, nil
}

func canonicalObservedNftJSONRule(rule nftJSONRule) ([]string, error) {
	if len(rule.Expr) == 0 {
		return nil, errors.New("rule has no statements")
	}
	statements := make([]string, 0, len(rule.Expr)+1)
	for _, raw := range rule.Expr {
		statement, err := canonicalObservedNftJSONStatement(raw)
		if err != nil {
			return nil, err
		}
		statements = append(statements, statement)
	}
	statements = normalizeImplicitNftDependencies(statements)
	statements = append(statements, "comment="+rule.Comment)
	return statements, nil
}

func canonicalObservedNftJSONStatement(raw json.RawMessage) (string, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return "", fmt.Errorf("invalid nftables rule statement: %w", err)
	}
	if len(object) != 1 {
		return "", fmt.Errorf("nftables rule statement has %d members, want 1", len(object))
	}
	for kind, body := range object {
		switch kind {
		case "match":
			return canonicalObservedNftJSONMatch(body)
		case "counter":
			return "counter", nil
		case planner.FirewallVerdictAccept:
			if !jsonValueIsNull(body) {
				return "", errors.New("accept verdict has unexpected payload")
			}
			return "accept", nil
		case planner.FirewallVerdictDrop:
			if !jsonValueIsNull(body) {
				return "", errors.New("drop verdict has unexpected payload")
			}
			return "drop", nil
		case planner.FirewallVerdictReject:
			return canonicalObservedNftJSONReject(body)
		default:
			return "", fmt.Errorf("unsupported nftables rule statement %q", kind)
		}
	}
	return "", errors.New("empty nftables rule statement")
}

func canonicalObservedNftJSONMatch(raw json.RawMessage) (string, error) {
	var match struct {
		Op    string          `json:"op"`
		Left  json.RawMessage `json:"left"`
		Right json.RawMessage `json:"right"`
	}
	if err := decodeStrictNftJSON(raw, &match); err != nil {
		return "", fmt.Errorf("invalid match statement: %w", err)
	}
	if match.Op != "==" && match.Op != "!=" {
		return "", fmt.Errorf("unsupported match operator %q", match.Op)
	}
	left, err := canonicalNftJSONLeft(match.Left)
	if err != nil {
		return "", err
	}
	right, err := canonicalNftJSONRight(left, match.Right)
	if err != nil {
		return "", err
	}
	return "match=" + left + match.Op + right, nil
}

func canonicalNftJSONLeft(raw json.RawMessage) (string, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return "", fmt.Errorf("invalid match left expression: %w", err)
	}
	if len(object) != 1 {
		return "", fmt.Errorf("match left expression has %d members, want 1", len(object))
	}
	for kind, body := range object {
		switch kind {
		case "meta":
			var meta struct {
				Key string `json:"key"`
			}
			if err := decodeStrictNftJSON(body, &meta); err != nil {
				return "", fmt.Errorf("invalid meta expression: %w", err)
			}
			switch meta.Key {
			case "oifname", "nfproto", "l4proto":
				return "meta:" + meta.Key + ":", nil
			default:
				return "", fmt.Errorf("unsupported meta key %q", meta.Key)
			}
		case "payload":
			var payload struct {
				Protocol string `json:"protocol"`
				Field    string `json:"field"`
			}
			if err := decodeStrictNftJSON(body, &payload); err != nil {
				return "", fmt.Errorf("invalid payload expression: %w", err)
			}
			switch payload.Protocol + ":" + payload.Field {
			case "ip:daddr", "udp:sport", "udp:dport", "icmpv6:type":
				return "payload:" + payload.Protocol + ":" + payload.Field + ":", nil
			default:
				return "", fmt.Errorf("unsupported payload expression %s.%s", payload.Protocol, payload.Field)
			}
		default:
			return "", fmt.Errorf("unsupported match left expression %q", kind)
		}
	}
	return "", errors.New("empty match left expression")
}

func canonicalNftJSONRight(left string, raw json.RawMessage) (string, error) {
	var setObject struct {
		Set []json.RawMessage `json:"set"`
	}
	if len(raw) > 0 && raw[0] == '{' {
		if err := decodeStrictNftJSON(raw, &setObject); err != nil {
			return "", fmt.Errorf("unsupported match right expression: %w", err)
		}
		if len(setObject.Set) == 0 {
			return "", errors.New("match set is empty")
		}
		values := make([]string, 0, len(setObject.Set))
		for _, item := range setObject.Set {
			value, err := canonicalNftJSONScalar(left, item)
			if err != nil {
				return "", err
			}
			values = append(values, value)
		}
		sort.Strings(values)
		return "{" + strings.Join(values, ",") + "}", nil
	}
	return canonicalNftJSONScalar(left, raw)
}

func canonicalNftJSONScalar(left string, raw json.RawMessage) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("invalid match scalar: %w", err)
	}
	switch typed := value.(type) {
	case string:
		return canonicalNftScalar(left, typed)
	case json.Number:
		return canonicalNftScalar(left, typed.String())
	default:
		return "", fmt.Errorf("unsupported match scalar type %T", value)
	}
}

func canonicalNftScalar(left, value string) (string, error) {
	value = strings.TrimSpace(value)
	switch left {
	case "meta:oifname:":
		if value == "" {
			return "", errors.New("empty output interface name")
		}
		return value, nil
	case "meta:nfproto:":
		switch value {
		case "ipv4", "2":
			return "ipv4", nil
		case "ipv6", "10":
			return "ipv6", nil
		default:
			return "", fmt.Errorf("unsupported nfproto %q", value)
		}
	case "meta:l4proto:":
		switch value {
		case "udp", "17":
			return "udp", nil
		case "icmpv6", "ipv6-icmp", "58":
			return "icmpv6", nil
		default:
			return "", fmt.Errorf("unsupported l4proto %q", value)
		}
	case "payload:ip:daddr:":
		ip := net.ParseIP(value)
		if ip == nil || ip.To4() == nil {
			return "", fmt.Errorf("invalid IPv4 destination %q", value)
		}
		return ip.To4().String(), nil
	case "payload:udp:sport:", "payload:udp:dport:":
		port, err := strconv.ParseUint(value, 10, 16)
		if err != nil {
			return "", fmt.Errorf("invalid UDP port %q", value)
		}
		return strconv.FormatUint(port, 10), nil
	case "payload:icmpv6:type:":
		return canonicalICMPv6Type(value)
	default:
		return "", fmt.Errorf("unsupported canonical match identity %q", left)
	}
}

func canonicalICMPv6Type(value string) (string, error) {
	switch strings.TrimSpace(value) {
	case "nd-router-solicit", "133":
		return "133", nil
	case "nd-router-advert", "134":
		return "134", nil
	case "nd-neighbor-solicit", "135":
		return "135", nil
	case "nd-neighbor-advert", "136":
		return "136", nil
	default:
		return "", fmt.Errorf("unsupported ICMPv6 type %q", value)
	}
}

func canonicalObservedNftJSONReject(raw json.RawMessage) (string, error) {
	if jsonValueIsNull(raw) {
		return "reject", nil
	}
	var reject struct {
		Type string          `json:"type"`
		Expr json.RawMessage `json:"expr"`
	}
	if err := decodeStrictNftJSON(raw, &reject); err != nil {
		return "", fmt.Errorf("invalid reject statement: %w", err)
	}
	if reject.Type != "" && reject.Type != "icmpx" {
		return "", fmt.Errorf("non-default reject type %q", reject.Type)
	}
	if len(reject.Expr) != 0 && !jsonValueIsNull(reject.Expr) {
		value, err := canonicalRawScalar(reject.Expr)
		if err != nil {
			return "", err
		}
		if value != "port-unreachable" && value != "1" {
			return "", fmt.Errorf("non-default reject expression %q", value)
		}
	}
	return "reject", nil
}

func canonicalRawScalar(raw json.RawMessage) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("invalid scalar: %w", err)
	}
	switch typed := value.(type) {
	case string:
		return typed, nil
	case json.Number:
		return typed.String(), nil
	default:
		return "", fmt.Errorf("unsupported scalar type %T", value)
	}
}

func jsonValueIsNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}

func normalizeImplicitNftDependencies(statements []string) []string {
	hasUDP := false
	hasICMPv6 := false
	hasIPv4Payload := false
	for _, statement := range statements {
		switch {
		case strings.HasPrefix(statement, "match=payload:udp:"):
			hasUDP = true
		case strings.HasPrefix(statement, "match=payload:icmpv6:"):
			hasICMPv6 = true
		case strings.HasPrefix(statement, "match=payload:ip:"):
			hasIPv4Payload = true
		}
	}
	out := make([]string, 0, len(statements))
	for _, statement := range statements {
		switch {
		case hasUDP && statement == "match=meta:l4proto:==udp":
			continue
		case hasICMPv6 && statement == "match=meta:l4proto:==icmpv6":
			continue
		case hasICMPv6 && statement == "match=meta:nfproto:==ipv6":
			continue
		case hasIPv4Payload && statement == "match=meta:nfproto:==ipv4":
			continue
		default:
			out = append(out, statement)
		}
	}
	return out
}

func canonicalPlannedNftRule(rule planner.TunFirewallRulePlan) ([]string, error) {
	statements, err := canonicalPlannedNftExpression(rule.Expr)
	if err != nil {
		return nil, err
	}
	statements = append(statements, "counter")
	switch rule.Verdict {
	case planner.FirewallVerdictAccept, planner.FirewallVerdictDrop:
		statements = append(statements, rule.Verdict)
	case planner.FirewallVerdictReject:
		statements = append(statements, "reject")
	default:
		return nil, fmt.Errorf("unsupported nftables verdict %q", rule.Verdict)
	}
	statements = normalizeImplicitNftDependencies(statements)
	statements = append(statements, "comment="+rule.Ownership)
	return statements, nil
}

func canonicalPlannedNftExpression(expr string) ([]string, error) {
	fields := nftExpressionFields(expr)
	var statements []string
	for i := 0; i < len(fields); {
		switch fields[i] {
		case "oifname":
			if i+1 >= len(fields) {
				return nil, errors.New("incomplete oifname expression")
			}
			op := "=="
			valueIndex := i + 1
			if fields[i+1] == "!=" {
				op = "!="
				valueIndex++
			}
			if valueIndex >= len(fields) {
				return nil, errors.New("incomplete oifname comparison")
			}
			value, err := canonicalNftScalar("meta:oifname:", fields[valueIndex])
			if err != nil {
				return nil, err
			}
			statements = append(statements, "match=meta:oifname:"+op+value)
			i = valueIndex + 1
		case "ip":
			if i+2 >= len(fields) || fields[i+1] != "daddr" {
				return nil, fmt.Errorf("unsupported IPv4 nft expression near %q", strings.Join(fields[i:], " "))
			}
			value, err := canonicalNftScalar("payload:ip:daddr:", fields[i+2])
			if err != nil {
				return nil, err
			}
			statements = append(statements, "match=payload:ip:daddr:=="+value)
			i += 3
		case "meta":
			if i+2 >= len(fields) || fields[i+1] != "nfproto" {
				return nil, fmt.Errorf("unsupported meta nft expression near %q", strings.Join(fields[i:], " "))
			}
			value, err := canonicalNftScalar("meta:nfproto:", fields[i+2])
			if err != nil {
				return nil, err
			}
			statements = append(statements, "match=meta:nfproto:=="+value)
			i += 3
		case "udp":
			if i+2 >= len(fields) || (fields[i+1] != "sport" && fields[i+1] != "dport") {
				return nil, fmt.Errorf("unsupported UDP nft expression near %q", strings.Join(fields[i:], " "))
			}
			left := "payload:udp:" + fields[i+1] + ":"
			value, err := canonicalNftScalar(left, fields[i+2])
			if err != nil {
				return nil, err
			}
			statements = append(statements, "match="+left+"=="+value)
			i += 3
		case "icmpv6":
			if i+3 >= len(fields) || fields[i+1] != "type" || fields[i+2] != "{" {
				return nil, fmt.Errorf("unsupported ICMPv6 nft expression near %q", strings.Join(fields[i:], " "))
			}
			var values []string
			i += 3
			for i < len(fields) && fields[i] != "}" {
				value := strings.TrimSuffix(fields[i], ",")
				canonical, err := canonicalICMPv6Type(value)
				if err != nil {
					return nil, err
				}
				values = append(values, canonical)
				i++
			}
			if i >= len(fields) || fields[i] != "}" || len(values) == 0 {
				return nil, errors.New("unterminated or empty ICMPv6 type set")
			}
			sort.Strings(values)
			statements = append(statements, "match=payload:icmpv6:type:=={"+strings.Join(values, ",")+"}")
			i++
		default:
			return nil, fmt.Errorf("unsupported nftables expression near %q", strings.Join(fields[i:], " "))
		}
	}
	return statements, nil
}

func verifyExactNftJSONChains(observed []observedNftJSONChain, plan planner.TunFirewallPlan) error {
	var expectedChains []planner.TunFirewallChainPlan
	for _, chain := range plan.Chains {
		if chain.Action == planner.FirewallTableAction || chain.Action == planner.FirewallActionAdd {
			expectedChains = append(expectedChains, chain)
		}
	}
	if len(observed) != len(expectedChains) {
		return fmt.Errorf("chain cardinality=%d, want %d", len(observed), len(expectedChains))
	}
	for i, expected := range expectedChains {
		got := observed[i]
		if got.name != expected.Name || got.typeName != expected.Type || got.hook != expected.Hook || got.priority != expected.Priority || got.policy != expected.Policy {
			return fmt.Errorf("chain[%d] metadata mismatch: got name=%s type=%s hook=%s priority=%d policy=%s", i, got.name, got.typeName, got.hook, got.priority, got.policy)
		}
		var expectedRules [][]string
		for _, rule := range plan.Rules {
			if rule.Action != planner.FirewallActionAdd || rule.Chain != expected.Name {
				continue
			}
			canonical, err := canonicalPlannedNftRule(rule)
			if err != nil {
				return fmt.Errorf("canonicalize planned chain %s rule[%d]: %w", expected.Name, len(expectedRules), err)
			}
			expectedRules = append(expectedRules, canonical)
		}
		if len(got.rules) != len(expectedRules) {
			return fmt.Errorf("chain %s rule cardinality=%d, want %d", expected.Name, len(got.rules), len(expectedRules))
		}
		for ruleIndex := range expectedRules {
			if !equalStringFields(got.rules[ruleIndex], expectedRules[ruleIndex]) {
				return fmt.Errorf("chain %s rule[%d] mismatch", expected.Name, ruleIndex)
			}
		}
	}
	return nil
}
