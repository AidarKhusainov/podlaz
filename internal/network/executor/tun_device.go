package executor

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

const (
	defaultTunDeviceUser  = "podlaz-xray"
	defaultTunDeviceGroup = "podlaz-xray"
)

type IPTunDeviceExecutor struct {
	Runner      CommandRunner
	DeviceUser  string
	DeviceGroup string
}

type tunDeviceCreationProof struct {
	Name             string
	LinkIndex        int
	LinkKind         string
	PreExistingAbsent bool
}

func (e IPTunDeviceExecutor) Create(ctx context.Context, plan planner.TunDevicePlan) (Step, error) {
	if plan.Name == "" {
		return Step{}, errors.New("missing TUN device name")
	}
	args := []string{"tuntap", "add", "dev", plan.Name, "mode", "tun"}
	if user := strings.TrimSpace(e.DeviceUser); user != "" {
		args = append(args, "user", user)
	}
	if group := strings.TrimSpace(e.DeviceGroup); group != "" {
		args = append(args, "group", group)
	}
	if err := e.run(ctx, "ip", args...); err != nil {
		return Step{}, fmt.Errorf("create TUN device %s: %w", plan.Name, err)
	}
	proof, err := e.inspectCreationProof(ctx, plan.Name)
	if err != nil {
		step := Step{Kind: "tun-device", Target: plan.Name, Description: tunDeviceOwnershipDescription(plan.Reason, tunDeviceCreationProof{Name: plan.Name, PreExistingAbsent: true}), Owner: OwnerTunDevice}
		return step, fmt.Errorf("inspect created TUN device %s identity: %w", plan.Name, err)
	}
	step := Step{Kind: "tun-device", Target: plan.Name, Description: tunDeviceOwnershipDescription(plan.Reason, proof), Owner: OwnerTunDevice}
	if plan.MTU > 0 {
		if err := e.run(ctx, "ip", "link", "set", "dev", plan.Name, "mtu", strconv.Itoa(plan.MTU)); err != nil {
			return step, fmt.Errorf("set TUN device %s MTU: %w", plan.Name, err)
		}
	}
	if err := e.run(ctx, "ip", "link", "set", "dev", plan.Name, "up"); err != nil {
		return step, fmt.Errorf("bring TUN device %s up: %w", plan.Name, err)
	}
	return step, nil
}

func (e IPTunDeviceExecutor) Verify(ctx context.Context, plan planner.TunDevicePlan) error {
	if strings.TrimSpace(plan.Name) == "" {
		return errors.New("missing TUN device name")
	}
	result, err := observeCommand(ctx, e.Runner, "ip", "-details", "link", "show", "dev", plan.Name)
	if err != nil {
		return fmt.Errorf("verify TUN device %s: %w", plan.Name, err)
	}
	if err := verifyTUNLinkDetails(plan, result.Stdout); err != nil {
		return fmt.Errorf("verify TUN device %s: %w", plan.Name, err)
	}
	return nil
}

func verifyTUNLinkDetails(plan planner.TunDevicePlan, output string) error {
	text := strings.TrimSpace(output)
	if text == "" {
		return errors.New("empty ip link details output")
	}
	if !tunLinkOutputIsTun(text) {
		return fmt.Errorf("link is not a TUN device: %s", firstLine(text))
	}
	if plan.MTU > 0 && !linkOutputHasMTU(text, plan.MTU) {
		return fmt.Errorf("link MTU does not match planned MTU %d: %s", plan.MTU, firstLine(text))
	}
	if !linkOutputIsUp(text) {
		return fmt.Errorf("link is not up: %s", firstLine(text))
	}
	return nil
}

func tunLinkOutputIsTun(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		for i := 0; i+2 < len(fields); i++ {
			if fields[i] == "tun" && fields[i+1] == "type" && fields[i+2] == "tun" {
				return true
			}
		}
	}
	return false
}

func linkOutputHasMTU(output string, mtu int) bool {
	want := strconv.Itoa(mtu)
	for _, fieldList := range strings.Split(output, "\n") {
		fields := strings.Fields(fieldList)
		for i := 0; i+1 < len(fields); i++ {
			if fields[i] == "mtu" && fields[i+1] == want {
				return true
			}
		}
	}
	return false
}

func linkOutputIsUp(output string) bool {
	line := firstLine(output)
	if strings.Contains(line, "state UP") {
		return true
	}
	start := strings.Index(line, "<")
	end := strings.Index(line, ">")
	if start < 0 || end <= start {
		return false
	}
	flags := strings.Split(line[start+1:end], ",")
	for _, flag := range flags {
		if strings.TrimSpace(flag) == "UP" {
			return true
		}
	}
	return false
}

func firstLine(output string) string {
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return "<empty>"
}

func (e IPTunDeviceExecutor) Rollback(ctx context.Context, plan planner.TunDevicePlan) error {
	if plan.Name == "" {
		return nil
	}
	proof, ok := parseTunDeviceOwnershipDescription(plan.Name, plan.Reason)
	if !ok || proof.LinkIndex <= 0 || proof.LinkKind != "tun" || !proof.PreExistingAbsent {
		return fmt.Errorf("refuse TUN device rollback without exact creation proof for %s", plan.Name)
	}
	current, err := e.inspectCreationProof(ctx, plan.Name)
	if err != nil {
		if resourceMissing(err) {
			return nil
		}
		return fmt.Errorf("inspect TUN device %s before rollback: %w", plan.Name, err)
	}
	if current.Name != proof.Name || current.LinkIndex != proof.LinkIndex || current.LinkKind != proof.LinkKind {
		return fmt.Errorf("refuse TUN device rollback: current identity name=%s ifindex=%d kind=%s does not match created identity name=%s ifindex=%d kind=%s", current.Name, current.LinkIndex, current.LinkKind, proof.Name, proof.LinkIndex, proof.LinkKind)
	}
	if err := e.run(ctx, "ip", "link", "del", "dev", plan.Name); err != nil && !resourceMissing(err) {
		return fmt.Errorf("delete TUN device %s: %w", plan.Name, err)
	}
	return nil
}

func (e IPTunDeviceExecutor) inspectCreationProof(ctx context.Context, name string) (tunDeviceCreationProof, error) {
	result, err := observeCommand(ctx, e.Runner, "ip", "-details", "-o", "link", "show", "dev", name)
	if err != nil {
		return tunDeviceCreationProof{}, err
	}
	proof, ok := parseTunDeviceCreationProof(result.Stdout)
	if !ok || proof.Name != name || proof.LinkKind != "tun" || proof.LinkIndex <= 0 {
		return tunDeviceCreationProof{}, fmt.Errorf("created link identity is not exact TUN proof: %s", firstLine(result.Stdout))
	}
	proof.PreExistingAbsent = true
	return proof, nil
}

func parseTunDeviceCreationProof(output string) (tunDeviceCreationProof, bool) {
	line := firstLine(output)
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return tunDeviceCreationProof{}, false
	}
	index, err := strconv.Atoi(strings.TrimSuffix(fields[0], ":"))
	if err != nil || index <= 0 {
		return tunDeviceCreationProof{}, false
	}
	name := strings.TrimSuffix(fields[1], ":")
	name = strings.Split(name, "@")[0]
	kind := ""
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "type" && fields[i+1] == "tun" {
			kind = "tun"
			break
		}
	}
	return tunDeviceCreationProof{Name: name, LinkIndex: index, LinkKind: kind}, name != "" && kind != ""
}

func tunDeviceOwnershipDescription(reason string, proof tunDeviceCreationProof) string {
	parts := []string{}
	if strings.TrimSpace(reason) != "" {
		parts = append(parts, strings.TrimSpace(reason))
	}
	parts = append(parts,
		"creation-proof-name="+proof.Name,
		"creation-proof-ifindex="+strconv.Itoa(proof.LinkIndex),
		"creation-proof-kind="+proof.LinkKind,
		fmt.Sprintf("creation-proof-pre-existing-absent=%t", proof.PreExistingAbsent),
	)
	return strings.Join(parts, "; ")
}

func parseTunDeviceOwnershipDescription(expectedName, description string) (tunDeviceCreationProof, bool) {
	proof := tunDeviceCreationProof{Name: expectedName}
	for _, part := range strings.Split(description, ";") {
		part = strings.TrimSpace(part)
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "creation-proof-name":
			proof.Name = strings.TrimSpace(value)
		case "creation-proof-ifindex":
			index, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return tunDeviceCreationProof{}, false
			}
			proof.LinkIndex = index
		case "creation-proof-kind":
			proof.LinkKind = strings.TrimSpace(value)
		case "creation-proof-pre-existing-absent":
			proof.PreExistingAbsent = strings.TrimSpace(value) == "true"
		}
	}
	return proof, proof.Name == expectedName && proof.LinkIndex > 0 && proof.LinkKind == "tun" && proof.PreExistingAbsent
}

func (e IPTunDeviceExecutor) run(ctx context.Context, name string, args ...string) error {
	return runCommand(ctx, e.Runner, name, args...)
}
