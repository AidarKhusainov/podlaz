package executor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"github.com/google/nftables/userdata"
	"golang.org/x/sys/unix"
)

const nftCoherentObservationAttempts = 3

type nftMutationTarget struct {
	Family     string
	Table      string
	Handle     uint64
	Generation uint32
}

// nftMutationBackend is deliberately a concrete private seam. It exists only
// below the existing executors so unit tests can prove generation/transaction
// behavior without creating a second public firewall abstraction.
type nftMutationBackend struct {
	getGeneration func(context.Context) (uint32, error)
	removeTable   func(context.Context, nftMutationTarget) error
	replaceTable  func(context.Context, nftMutationTarget, planner.TunFirewallPlan) error
}

func defaultNftMutationBackend() *nftMutationBackend {
	return &nftMutationBackend{
		getGeneration: realNftGeneration,
		removeTable:   realNftRemoveTable,
		replaceTable:  realNftReplaceTable,
	}
}

func (b *nftMutationBackend) generation(ctx context.Context) (uint32, error) {
	if b == nil || b.getGeneration == nil {
		return 0, errors.New("nftables mutation backend has no generation reader")
	}
	return b.getGeneration(ctx)
}

func observeVerifiedNftTableForMutation(
	ctx context.Context,
	runner CommandRunner,
	backend *nftMutationBackend,
	family, table string,
	plan planner.TunFirewallPlan,
) (nftMutationTarget, bool, error) {
	for attempt := 0; attempt < nftCoherentObservationAttempts; attempt++ {
		before, err := backend.generation(ctx)
		if err != nil {
			return nftMutationTarget{}, false, fmt.Errorf("read nftables generation before observation: %w", err)
		}

		present, err := observeNftTablePresence(ctx, runner, family, table)
		if err != nil {
			return nftMutationTarget{}, false, err
		}
		if !present {
			after, err := backend.generation(ctx)
			if err != nil {
				return nftMutationTarget{}, false, fmt.Errorf("read nftables generation after absence observation: %w", err)
			}
			if before != after {
				continue
			}
			return nftMutationTarget{}, true, nil
		}

		result, err := observeCommand(ctx, runner, "nft", "-j", "list", "table", family, table)
		if err != nil {
			return nftMutationTarget{}, false, fmt.Errorf("observe nftables table %s %s for mutation: %w", family, table, err)
		}
		snapshot, err := parseNftTableJSON(result.Stdout, family, table)
		if err != nil {
			return nftMutationTarget{}, false, fmt.Errorf("decode nftables table %s %s for mutation: %w", family, table, err)
		}
		if err := verifyNftTableSnapshot(snapshot, plan); err != nil {
			return nftMutationTarget{}, false, fmt.Errorf("verify nftables table %s %s for mutation: %w", family, table, err)
		}
		after, err := backend.generation(ctx)
		if err != nil {
			return nftMutationTarget{}, false, fmt.Errorf("read nftables generation after observation: %w", err)
		}
		if before != after {
			continue
		}
		if snapshot.Handle == 0 {
			return nftMutationTarget{}, false, errors.New("verified nftables table has no kernel handle")
		}
		return nftMutationTarget{
			Family: family, Table: table, Handle: snapshot.Handle, Generation: before,
		}, false, nil
	}
	return nftMutationTarget{}, false, fmt.Errorf("nftables table %s %s changed during %d coherent observation attempts", family, table, nftCoherentObservationAttempts)
}

func realNftGeneration(ctx context.Context) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	conn, err := nftables.New()
	if err != nil {
		return 0, fmt.Errorf("open nftables netlink connection: %w", err)
	}
	generation, err := conn.GetGen()
	if err != nil {
		return 0, fmt.Errorf("read nftables generation: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return generation.ID, nil
}

func realNftRemoveTable(ctx context.Context, target nftMutationTarget) error {
	if err := validateNftMutationTarget(target); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	conn, err := nftables.New()
	if err != nil {
		return fmt.Errorf("open nftables netlink connection: %w", err)
	}
	conn.DelTable(&nftables.Table{Name: target.Table, Family: nftables.TableFamilyINet})
	if err := conn.FlushWithGenID(target.Generation); err != nil {
		return fmt.Errorf("commit generation-guarded nftables table removal: %w", err)
	}
	return ctx.Err()
}

func realNftReplaceTable(ctx context.Context, target nftMutationTarget, plan planner.TunFirewallPlan) error {
	if err := validateNftMutationTarget(target); err != nil {
		return err
	}
	if target.Family != plan.Family || target.Table != plan.Table {
		return errors.New("nftables replacement plan changes exact table identity")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	conn, err := nftables.New()
	if err != nil {
		return fmt.Errorf("open nftables netlink connection: %w", err)
	}
	oldTable := &nftables.Table{Name: target.Table, Family: nftables.TableFamilyINet}
	conn.DelTable(oldTable)
	newTable := conn.CreateTable(&nftables.Table{Name: target.Table, Family: nftables.TableFamilyINet})
	chains, err := addNftPlanChains(conn, newTable, plan)
	if err != nil {
		return err
	}
	if err := addNftPlanRules(conn, newTable, chains, plan); err != nil {
		return err
	}
	if err := conn.FlushWithGenID(target.Generation); err != nil {
		return fmt.Errorf("commit generation-guarded nftables table replacement: %w", err)
	}
	return ctx.Err()
}

func validateNftMutationTarget(target nftMutationTarget) error {
	if target.Family != ownedNFTFamily || strings.TrimSpace(target.Table) == "" {
		return fmt.Errorf("invalid nftables mutation target %s %s", target.Family, target.Table)
	}
	if target.Handle == 0 {
		return errors.New("nftables mutation target has no verified kernel handle")
	}
	return nil
}

func addNftPlanChains(conn *nftables.Conn, table *nftables.Table, plan planner.TunFirewallPlan) (map[string]*nftables.Chain, error) {
	chains := make(map[string]*nftables.Chain)
	for _, planned := range plan.Chains {
		if planned.Action != planner.FirewallTableAction && planned.Action != planner.FirewallActionAdd {
			continue
		}
		if planned.Type != planner.FirewallChainTypeFilter || planned.Hook != planner.FirewallOutputHook {
			return nil, fmt.Errorf("unsupported nftables replacement chain %s type=%s hook=%s", planned.Name, planned.Type, planned.Hook)
		}
		if _, duplicate := chains[planned.Name]; duplicate {
			return nil, fmt.Errorf("duplicate nftables replacement chain %q", planned.Name)
		}
		priority := nftables.ChainPriority(planned.Priority)
		var policy nftables.ChainPolicy
		switch planned.Policy {
		case planner.FirewallDefaultChainPolicy:
			policy = nftables.ChainPolicyAccept
		case planner.FirewallVerdictDrop:
			policy = nftables.ChainPolicyDrop
		default:
			return nil, fmt.Errorf("unsupported nftables replacement chain policy %q", planned.Policy)
		}
		chain := conn.AddChain(&nftables.Chain{
			Name: planned.Name, Table: table,
			Type: nftables.ChainTypeFilter, Hooknum: nftables.ChainHookOutput,
			Priority: &priority, Policy: &policy,
		})
		chains[planned.Name] = chain
	}
	return chains, nil
}

func addNftPlanRules(conn *nftables.Conn, table *nftables.Table, chains map[string]*nftables.Chain, plan planner.TunFirewallPlan) error {
	for _, planned := range plan.Rules {
		if planned.Action != planner.FirewallActionAdd {
			continue
		}
		chain, exists := chains[planned.Chain]
		if !exists {
			return fmt.Errorf("nftables replacement rule references unknown chain %q", planned.Chain)
		}
		expressions, err := nftRuleNetlinkExpressions(conn, table, planned)
		if err != nil {
			return fmt.Errorf("encode nftables replacement rule in chain %s: %w", planned.Chain, err)
		}
		expressions = append(expressions, &expr.Counter{})
		switch planned.Verdict {
		case planner.FirewallVerdictAccept:
			expressions = append(expressions, &expr.Verdict{Kind: expr.VerdictAccept})
		case planner.FirewallVerdictDrop:
			expressions = append(expressions, &expr.Verdict{Kind: expr.VerdictDrop})
		case planner.FirewallVerdictReject:
			expressions = append(expressions, &expr.Reject{})
		default:
			return fmt.Errorf("unsupported nftables replacement verdict %q", planned.Verdict)
		}
		conn.AddRule(&nftables.Rule{
			Table: table, Chain: chain, Exprs: expressions,
			UserData: userdata.AppendString(nil, userdata.TypeComment, planned.Ownership),
		})
	}
	return nil
}

func nftRuleNetlinkExpressions(conn *nftables.Conn, table *nftables.Table, rule planner.TunFirewallRulePlan) ([]expr.Any, error) {
	fields := nftExpressionFields(rule.Expr)
	var out []expr.Any
	udpProtocolLoaded := false
	for i := 0; i < len(fields); {
		switch fields[i] {
		case "oifname":
			if i+1 >= len(fields) {
				return nil, errors.New("incomplete oifname expression")
			}
			op := expr.CmpOpEq
			valueIndex := i + 1
			if fields[i+1] == "!=" {
				op = expr.CmpOpNeq
				valueIndex++
			}
			if valueIndex >= len(fields) {
				return nil, errors.New("incomplete oifname comparison")
			}
			out = append(out,
				&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1},
				&expr.Cmp{Op: op, Register: 1, Data: nftIfname(fields[valueIndex])},
			)
			i = valueIndex + 1
		case "ip":
			if i+2 >= len(fields) || fields[i+1] != "daddr" {
				return nil, fmt.Errorf("unsupported IPv4 expression near %q", strings.Join(fields[i:], " "))
			}
			ip := net.ParseIP(fields[i+2])
			if ip == nil || ip.To4() == nil {
				return nil, fmt.Errorf("invalid IPv4 destination %q", fields[i+2])
			}
			out = append(out,
				&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
				&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{byte(unix.NFPROTO_IPV4)}},
				&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 16, Len: 4},
				&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: ip.To4()},
			)
			i += 3
		case "meta":
			if i+2 >= len(fields) || fields[i+1] != "nfproto" {
				return nil, fmt.Errorf("unsupported meta expression near %q", strings.Join(fields[i:], " "))
			}
			var family byte
			switch fields[i+2] {
			case "ipv4":
				family = byte(unix.NFPROTO_IPV4)
			case "ipv6":
				family = byte(unix.NFPROTO_IPV6)
			default:
				return nil, fmt.Errorf("unsupported nfproto %q", fields[i+2])
			}
			out = append(out,
				&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
				&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{family}},
			)
			i += 3
		case "udp":
			if i+2 >= len(fields) || (fields[i+1] != "sport" && fields[i+1] != "dport") {
				return nil, fmt.Errorf("unsupported UDP expression near %q", strings.Join(fields[i:], " "))
			}
			if !udpProtocolLoaded {
				out = append(out,
					&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
					&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{byte(unix.IPPROTO_UDP)}},
				)
				udpProtocolLoaded = true
			}
			port, err := parseNftPort(fields[i+2])
			if err != nil {
				return nil, err
			}
			offset := uint32(0)
			if fields[i+1] == "dport" {
				offset = 2
			}
			out = append(out,
				&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: offset, Len: 2},
				&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.BigEndian.PutUint16(port)},
			)
			i += 3
		case "icmpv6":
			if i+3 >= len(fields) || fields[i+1] != "type" || fields[i+2] != "{" {
				return nil, fmt.Errorf("unsupported ICMPv6 expression near %q", strings.Join(fields[i:], " "))
			}
			var values []nftables.SetElement
			i += 3
			for i < len(fields) && fields[i] != "}" {
				value := strings.TrimSuffix(fields[i], ",")
				icmpType, err := nftICMPv6Type(value)
				if err != nil {
					return nil, err
				}
				values = append(values, nftables.SetElement{Key: []byte{icmpType}})
				i++
			}
			if i >= len(fields) || fields[i] != "}" || len(values) == 0 {
				return nil, errors.New("unterminated or empty ICMPv6 type set")
			}
			set := &nftables.Set{Table: table, Anonymous: true, Constant: true, KeyType: nftables.TypeICMP6Type}
			if err := conn.AddSet(set, values); err != nil {
				return nil, fmt.Errorf("add anonymous ICMPv6 type set: %w", err)
			}
			out = append(out,
				&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
				&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{byte(unix.NFPROTO_IPV6)}},
				&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
				&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{byte(unix.IPPROTO_ICMPV6)}},
				&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 0, Len: 1},
				&expr.Lookup{SourceRegister: 1, SetName: set.Name, SetID: set.ID},
			)
			i++
		default:
			return nil, fmt.Errorf("unsupported nftables expression near %q", strings.Join(fields[i:], " "))
		}
	}
	return out, nil
}

func nftIfname(name string) []byte {
	value := make([]byte, 16)
	copy(value, name+"\x00")
	return value
}

func parseNftPort(value string) (uint16, error) {
	var port uint64
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("invalid UDP port %q", value)
		}
		port = port*10 + uint64(r-'0')
		if port > 65535 {
			return 0, fmt.Errorf("invalid UDP port %q", value)
		}
	}
	return uint16(port), nil
}

func nftICMPv6Type(value string) (byte, error) {
	switch strings.TrimSpace(value) {
	case "nd-router-solicit", "133":
		return 133, nil
	case "nd-neighbor-solicit", "135":
		return 135, nil
	case "nd-neighbor-advert", "136":
		return 136, nil
	default:
		return 0, fmt.Errorf("unsupported ICMPv6 type %q", value)
	}
}
