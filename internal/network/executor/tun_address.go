package executor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"golang.org/x/sys/unix"
)

const (
	OwnerTunAddress                   = "podlaz:tun-address"
	defaultTunAddressBindAttempts     = 30
	defaultTunAddressBindPollInterval = 100 * time.Millisecond
)

var (
	ErrTunAddressConflict      = errors.New("TUN address conflict")
	ErrTunAddressApply         = errors.New("TUN address apply failure")
	ErrTunAddressVerify        = errors.New("TUN address verification failure")
	ErrTunLinkIdentityMismatch = errors.New("TUN link identity mismatch")
)

type TunLinkCreationProof struct {
	PreStartAbsent bool
	TrackedCorePID int
	CoreDone       <-chan struct{}
}

type TunAddressExecutor interface {
	Bind(context.Context, planner.TunAddressPlan, TunLinkCreationProof) (planner.TunAddressPlan, error)
	Apply(context.Context, planner.TunAddressPlan) (Step, error)
	Verify(context.Context, planner.TunAddressPlan) error
	Rollback(context.Context, planner.TunAddressPlan) error
}

type IPTunAddressExecutor struct {
	Runner                      CommandRunner
	BindAttempts                int
	BindPollInterval            time.Duration
	Sleep                       func(context.Context, time.Duration) error
	AllocationEvidenceCollector func(context.Context) (netsnapshot.TunAllocationEvidence, error)
}

func (e IPTunAddressExecutor) Bind(ctx context.Context, plan planner.TunAddressPlan, proof TunLinkCreationProof) (bound planner.TunAddressPlan, err error) {
	bound = plan
	defer func() {
		if err != nil && !errors.Is(err, ErrTunAddressVerify) {
			err = fmt.Errorf("%w: %w", ErrTunAddressVerify, err)
		}
	}()
	if err := validateTunAddressIntent(plan); err != nil {
		return bound, err
	}
	if err := validateTunLinkCreationProof(proof); err != nil {
		return bound, err
	}
	attempts := e.BindAttempts
	if attempts <= 0 {
		attempts = defaultTunAddressBindAttempts
	}
	interval := e.BindPollInterval
	if interval <= 0 {
		interval = defaultTunAddressBindPollInterval
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if err := requireTrackedCoreRunning(proof); err != nil {
			return bound, err
		}
		identity, err := e.inspectIdentity(ctx, plan.Interface)
		if err == nil {
			if identity.Kind != "tun" {
				return bound, fmt.Errorf("%w: %s is %s, expected tun", ErrTunLinkIdentityMismatch, plan.Interface, identity.Kind)
			}
			confirmed, confirmErr := e.inspectIdentity(ctx, plan.Interface)
			if confirmErr != nil {
				return bound, fmt.Errorf("revalidate appeared TUN link %s: %w", plan.Interface, confirmErr)
			}
			if confirmed.Index != identity.Index || confirmed.Kind != identity.Kind {
				return bound, fmt.Errorf("%w: TUN link %s changed while binding from index=%d kind=%s to index=%d kind=%s", ErrTunLinkIdentityMismatch, plan.Interface, identity.Index, identity.Kind, confirmed.Index, confirmed.Kind)
			}
			if err := requireTrackedCoreRunning(proof); err != nil {
				return bound, err
			}
			plan.LinkIndex = identity.Index
			plan.LinkKind = identity.Kind
			plan.AppearedAfterCore = true
			return plan, nil
		}
		lastErr = err
		if !resourceMissing(err) || attempt+1 >= attempts {
			break
		}
		if err := e.sleep(ctx, interval); err != nil {
			return bound, err
		}
	}
	return bound, fmt.Errorf("identify Xray-created TUN link %s: %w", plan.Interface, lastErr)
}

func (e IPTunAddressExecutor) Apply(ctx context.Context, plan planner.TunAddressPlan) (step Step, err error) {
	defer func() {
		if err == nil || errors.Is(err, ErrTunAddressConflict) || errors.Is(err, ErrTunAddressApply) {
			return
		}
		if errors.Is(err, ErrTunLinkIdentityMismatch) {
			err = fmt.Errorf("%w: %w", ErrTunAddressVerify, err)
			return
		}
		err = fmt.Errorf("%w: %w", ErrTunAddressApply, err)
	}()
	if err := validateBoundTunAddressPlan(plan); err != nil {
		return step, err
	}
	if err := e.verifyBoundIdentityFence(ctx, plan, "before address inventory", false); err != nil {
		return Step{}, err
	}
	addresses, err := e.inspectAddresses(ctx, plan.Interface)
	if err != nil {
		return Step{}, fmt.Errorf("inspect TUN addresses before apply: %w", err)
	}
	if err := e.verifyBoundIdentityFence(ctx, plan, "after address inventory", false); err != nil {
		return Step{}, err
	}
	exact, conflicts := addressInventoryState(addresses, plan)
	if conflicts > 0 || (exact > 0 && !plan.AllowOwnedExisting) {
		return Step{}, fmt.Errorf("%w: %s already has IPv4 address state that is not authorized by this transaction", ErrTunAddressConflict, plan.Interface)
	}
	if exact > 1 {
		return Step{}, fmt.Errorf("%w: exact address %s appears %d times", ErrTunAddressConflict, plan.CIDR, exact)
	}
	if err := e.verifyGlobalTunAddressAllocation(ctx, plan, exact, "before address apply"); err != nil {
		return Step{}, err
	}
	if exact == 0 {
		if err := e.verifyBoundIdentityFence(ctx, plan, "immediately before address apply", false); err != nil {
			return Step{}, err
		}
		step = tunAddressStep(plan)
		if err := runCommand(ctx, e.Runner, "ip", "-4", "address", "replace", plan.CIDR, "dev", plan.Interface); err != nil {
			return step, fmt.Errorf("assign TUN address %s to %s: %w", plan.CIDR, plan.Interface, err)
		}
		if err := e.verifyAddressPresence(ctx, plan, 1, "after address apply"); err != nil {
			return step, err
		}
		if err := e.verifyGlobalTunAddressAllocation(ctx, plan, 1, "after address apply"); err != nil {
			return step, err
		}
	}
	if err := e.verifyBoundIdentityFence(ctx, plan, "immediately before link up", false); err != nil {
		return step, err
	}
	if err := runCommand(ctx, e.Runner, "ip", "link", "set", "dev", plan.Interface, "up"); err != nil {
		return step, fmt.Errorf("bring addressed TUN link %s up: %w", plan.Interface, err)
	}
	if err := e.Verify(ctx, plan); err != nil {
		return step, fmt.Errorf("verify TUN address after apply: %w", err)
	}
	return step, nil
}

func (e IPTunAddressExecutor) Verify(ctx context.Context, plan planner.TunAddressPlan) (err error) {
	defer func() {
		if err != nil && !errors.Is(err, ErrTunAddressVerify) {
			err = fmt.Errorf("%w: %w", ErrTunAddressVerify, err)
		}
	}()
	if err := validateBoundTunAddressPlan(plan); err != nil {
		return err
	}
	if err := e.verifyBoundIdentityFence(ctx, plan, "before address verification inventory", true); err != nil {
		return err
	}
	addresses, err := e.inspectAddresses(ctx, plan.Interface)
	if err != nil {
		return fmt.Errorf("inspect TUN addresses during verification: %w", err)
	}
	if err := e.verifyBoundIdentityFence(ctx, plan, "after address verification inventory", true); err != nil {
		return err
	}
	exact, conflicts := addressInventoryState(addresses, plan)
	if conflicts != 0 {
		return fmt.Errorf("TUN link %s has %d conflicting IPv4 address entries", plan.Interface, conflicts)
	}
	if exact != 1 {
		return fmt.Errorf("TUN address %s must exist exactly once on %s; found %d", plan.CIDR, plan.Interface, exact)
	}
	for _, address := range addresses {
		if address.Interface == plan.Interface && address.Family == "ipv4" && address.CIDR == plan.CIDR && plan.Scope != "" && address.Scope != plan.Scope {
			return fmt.Errorf("TUN address %s has scope %s, expected %s", plan.CIDR, address.Scope, plan.Scope)
		}
	}
	if err := e.verifyGlobalTunAddressAllocation(ctx, plan, 1, "during address verification"); err != nil {
		return err
	}
	return e.verifyBoundIdentityFence(ctx, plan, "after address verification", true)
}

func (e IPTunAddressExecutor) Rollback(ctx context.Context, plan planner.TunAddressPlan) error {
	if strings.TrimSpace(plan.CIDR) == "" || strings.TrimSpace(plan.Interface) == "" {
		return nil
	}
	if err := validateBoundTunAddressPlan(plan); err != nil {
		return err
	}
	if err := e.verifyBoundIdentityFence(ctx, plan, "before address rollback inventory", false); err != nil {
		if plan.AllowMissingLink && resourceMissing(err) {
			return nil
		}
		return err
	}
	addresses, err := e.inspectAddresses(ctx, plan.Interface)
	if err != nil {
		return fmt.Errorf("inspect TUN addresses before rollback: %w", err)
	}
	if err := e.verifyBoundIdentityFence(ctx, plan, "after address rollback inventory", false); err != nil {
		return err
	}
	exact := 0
	for _, address := range addresses {
		if address.Interface == plan.Interface && address.CIDR == plan.CIDR {
			exact++
		}
	}
	if exact == 0 {
		return nil
	}
	if exact != 1 {
		return fmt.Errorf("refuse TUN address rollback: %s appears %d times on %s", plan.CIDR, exact, plan.Interface)
	}
	if err := e.verifyBoundIdentityFence(ctx, plan, "immediately before address rollback", false); err != nil {
		return err
	}
	if err := runCommand(ctx, e.Runner, "ip", "-4", "address", "del", plan.CIDR, "dev", plan.Interface); err != nil && !resourceMissing(err) {
		return fmt.Errorf("remove exact TUN address %s from %s: %w", plan.CIDR, plan.Interface, err)
	}
	return e.verifyAddressPresence(ctx, plan, 0, "after address rollback")
}

type tunLinkIdentity struct {
	Index int
	Kind  string
	Up    bool
}

func (e IPTunAddressExecutor) verifyBoundIdentityFence(ctx context.Context, plan planner.TunAddressPlan, phase string, requireUp bool) error {
	identity, err := e.inspectIdentity(ctx, plan.Interface)
	if err != nil {
		return fmt.Errorf("inspect TUN link %s: %w", phase, err)
	}
	if err := verifyBoundIdentity(plan, identity, requireUp); err != nil {
		return fmt.Errorf("%s: %w", phase, err)
	}
	return nil
}

func (e IPTunAddressExecutor) verifyAddressPresence(ctx context.Context, plan planner.TunAddressPlan, wantExact int, phase string) error {
	if err := e.verifyBoundIdentityFence(ctx, plan, phase+" identity", false); err != nil {
		return err
	}
	addresses, err := e.inspectAddresses(ctx, plan.Interface)
	if err != nil {
		return fmt.Errorf("inspect TUN addresses %s: %w", phase, err)
	}
	if err := e.verifyBoundIdentityFence(ctx, plan, phase+" identity after inventory", false); err != nil {
		return err
	}
	exact, conflicts := addressInventoryState(addresses, plan)
	if conflicts != 0 {
		return fmt.Errorf("TUN link %s has %d conflicting IPv4 address entries %s", plan.Interface, conflicts, phase)
	}
	if exact != wantExact {
		return fmt.Errorf("TUN address %s exact entries %s = %d, want %d", plan.CIDR, phase, exact, wantExact)
	}
	return nil
}

func (e IPTunAddressExecutor) verifyGlobalTunAddressAllocation(ctx context.Context, plan planner.TunAddressPlan, wantOwnExact int, phase string) error {
	if !planner.IsTunAddressExclusiveAction(plan.Action) {
		return nil
	}
	candidate, err := netip.ParsePrefix(strings.TrimSpace(plan.CIDR))
	if err != nil || !candidate.Addr().Is4() {
		return fmt.Errorf("%w: invalid allocated TUN address %q", ErrTunAddressConflict, plan.CIDR)
	}
	candidate = candidate.Masked()

	evidence, err := e.collectAllocationEvidence(ctx)
	if err != nil {
		return fmt.Errorf("%w: inspect authoritative TUN allocation evidence %s: %v", ErrTunAddressConflict, phase, err)
	}

	ownExact := 0
	for _, address := range evidence.IPv4Addresses {
		if !address.IsValid() || !address.Addr().Is4() {
			return fmt.Errorf("%w: invalid IPv4 address allocation evidence %s", ErrTunAddressConflict, phase)
		}
		if !ipv4PrefixesOverlapForAllocation(address, candidate) {
			continue
		}
		if address == candidate {
			ownExact++
			continue
		}
		return fmt.Errorf("%w: allocated TUN address %s overlaps foreign address %s %s", ErrTunAddressConflict, plan.CIDR, address, phase)
	}
	if ownExact != wantOwnExact {
		return fmt.Errorf("%w: allocated TUN address %s has %d global exact entries %s, want %d", ErrTunAddressConflict, plan.CIDR, ownExact, phase, wantOwnExact)
	}

	for _, route := range evidence.IPv4Routes {
		if route.Default {
			continue
		}
		if !route.Destination.IsValid() || !route.Destination.Addr().Is4() {
			return fmt.Errorf("%w: invalid IPv4 route allocation evidence %s", ErrTunAddressConflict, phase)
		}
		if !ipv4PrefixesOverlapForAllocation(route.Destination, candidate) {
			continue
		}
		if wantOwnExact == 1 && kernelLocalRouteForTunAddressEvidence(route, plan, candidate) {
			continue
		}
		return fmt.Errorf("%w: allocated TUN address %s overlaps route %s table %d %s", ErrTunAddressConflict, plan.CIDR, route.Destination, route.Table, phase)
	}
	return nil
}

func (e IPTunAddressExecutor) collectAllocationEvidence(ctx context.Context) (netsnapshot.TunAllocationEvidence, error) {
	if e.AllocationEvidenceCollector != nil {
		return e.AllocationEvidenceCollector(ctx)
	}
	return netsnapshot.CollectTunAllocationEvidence(ctx)
}

func kernelLocalRouteForTunAddressEvidence(route netsnapshot.TunAllocationRoute, plan planner.TunAddressPlan, candidate netip.Prefix) bool {
	return route.Destination == candidate &&
		route.Table == unix.RT_TABLE_LOCAL &&
		route.Type == unix.RTN_LOCAL &&
		route.LinkIndex == plan.LinkIndex
}

func ipv4PrefixesOverlapForAllocation(left, right netip.Prefix) bool {
	if !left.IsValid() || !right.IsValid() || !left.Addr().Is4() || !right.Addr().Is4() {
		return false
	}
	return left.Contains(right.Addr()) || right.Contains(left.Addr())
}

func ipv4CIDRsOverlapForAllocation(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" || left == planner.IPv4DefaultRoute || left == "0.0.0.0/0" {
		return false
	}
	leftIP, leftNet, leftErr := net.ParseCIDR(left)
	rightIP, rightNet, rightErr := net.ParseCIDR(right)
	if leftErr != nil || rightErr != nil || leftIP.To4() == nil || rightIP.To4() == nil {
		return false
	}
	return leftNet.Contains(rightIP) || rightNet.Contains(leftIP)
}

func (e IPTunAddressExecutor) inspectIdentity(ctx context.Context, link string) (tunLinkIdentity, error) {
	result, err := observeCommand(ctx, e.Runner, "ip", "-details", "-o", "link", "show", "dev", link)
	if err != nil {
		return tunLinkIdentity{}, err
	}
	return parseTunLinkIdentity(result.Stdout)
}

func parseTunLinkIdentity(output string) (tunLinkIdentity, error) {
	line := firstLine(output)
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return tunLinkIdentity{}, errors.New("malformed ip link identity output")
	}
	index, err := strconv.Atoi(strings.TrimSuffix(fields[0], ":"))
	if err != nil || index <= 0 {
		return tunLinkIdentity{}, errors.New("malformed ip link index")
	}
	kind := "unknown"
	if tunLinkOutputIsTun(output) {
		kind = "tun"
	}
	return tunLinkIdentity{Index: index, Kind: kind, Up: linkOutputIsUp(output)}, nil
}

func (e IPTunAddressExecutor) inspectAddresses(ctx context.Context, link string) ([]netsnapshot.IPAddress, error) {
	result, err := observeCommand(ctx, e.Runner, "ip", "-4", "-o", "address", "show", "dev", link)
	if err != nil {
		return nil, err
	}
	return netsnapshot.ParseIPv4Addresses(result.Stdout)
}

func verifyBoundIdentity(plan planner.TunAddressPlan, got tunLinkIdentity, requireUp bool) error {
	if got.Index != plan.LinkIndex || got.Kind != plan.LinkKind {
		return fmt.Errorf("%w: expected %s index=%d kind=%s, got index=%d kind=%s", ErrTunLinkIdentityMismatch, plan.Interface, plan.LinkIndex, plan.LinkKind, got.Index, got.Kind)
	}
	if requireUp && !got.Up {
		return fmt.Errorf("TUN link %s index=%d is not operationally up", plan.Interface, plan.LinkIndex)
	}
	return nil
}

func validateTunAddressIntent(plan planner.TunAddressPlan) error {
	if strings.TrimSpace(plan.Interface) == "" {
		return errors.New("missing TUN address interface")
	}
	if strings.TrimSpace(plan.CIDR) == "" {
		return errors.New("missing TUN address CIDR")
	}
	ip, network, err := net.ParseCIDR(plan.CIDR)
	if err != nil || ip.To4() == nil {
		return fmt.Errorf("invalid TUN IPv4 CIDR %q", plan.CIDR)
	}
	ones, bits := network.Mask.Size()
	if bits != 32 || ones != 32 {
		return fmt.Errorf("TUN address must use an IPv4 /32, got %q", plan.CIDR)
	}
	if plan.LinkKind != "" && plan.LinkKind != "tun" {
		return fmt.Errorf("unsupported TUN link kind %q", plan.LinkKind)
	}
	return nil
}

func validateBoundTunAddressPlan(plan planner.TunAddressPlan) error {
	if err := validateTunAddressIntent(plan); err != nil {
		return err
	}
	if !planner.IsTunAddressAssignAction(plan.Action) {
		return fmt.Errorf("TUN address action %q is not mutable", plan.Action)
	}
	if plan.LinkIndex <= 0 || plan.LinkKind != "tun" || !plan.AppearedAfterCore {
		return errors.New("TUN address plan is missing verified Xray-created link identity")
	}
	if plan.Owner != "" && plan.Owner != OwnerTunAddress {
		return fmt.Errorf("unexpected TUN address owner %q", plan.Owner)
	}
	return nil
}

func addressInventoryState(addresses []netsnapshot.IPAddress, plan planner.TunAddressPlan) (exact int, conflicts int) {
	for _, address := range addresses {
		if address.Interface != plan.Interface || address.Family != "ipv4" {
			conflicts++
			continue
		}
		if address.CIDR == plan.CIDR {
			exact++
			continue
		}
		conflicts++
	}
	return exact, conflicts
}

func tunAddressStep(plan planner.TunAddressPlan) Step {
	return Step{Kind: "tun-address", Target: fmt.Sprintf("%s@ifindex=%d:%s", plan.Interface, plan.LinkIndex, plan.CIDR), Description: plan.Reason, Owner: OwnerTunAddress}
}

func (e IPTunAddressExecutor) sleep(ctx context.Context, delay time.Duration) error {
	if e.Sleep != nil {
		return e.Sleep(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func validateTunLinkCreationProof(proof TunLinkCreationProof) error {
	if !proof.PreStartAbsent {
		return errors.New("TUN link was not authoritatively absent before tracked Xray start")
	}
	if proof.TrackedCorePID <= 1 {
		return errors.New("tracked Xray process identity is missing")
	}
	if proof.CoreDone == nil {
		return errors.New("tracked Xray lifecycle channel is missing")
	}
	return requireTrackedCoreRunning(proof)
}

func requireTrackedCoreRunning(proof TunLinkCreationProof) error {
	select {
	case <-proof.CoreDone:
		return errors.New("tracked Xray process exited before TUN link identity was bound")
	default:
		return nil
	}
}
