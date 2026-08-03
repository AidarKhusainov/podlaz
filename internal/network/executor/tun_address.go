package executor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
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

type TunAddressExecutor interface {
	Bind(context.Context, planner.TunAddressPlan) (planner.TunAddressPlan, error)
	Apply(context.Context, planner.TunAddressPlan) (Step, error)
	Verify(context.Context, planner.TunAddressPlan) error
	Rollback(context.Context, planner.TunAddressPlan) error
}

type IPTunAddressExecutor struct {
	Runner           CommandRunner
	BindAttempts     int
	BindPollInterval time.Duration
	Sleep            func(context.Context, time.Duration) error
}

func (e IPTunAddressExecutor) Bind(ctx context.Context, plan planner.TunAddressPlan) (bound planner.TunAddressPlan, err error) {
	bound = plan
	defer func() {
		if err != nil && !errors.Is(err, ErrTunAddressVerify) {
			err = fmt.Errorf("%w: %w", ErrTunAddressVerify, err)
		}
	}()
	if err := validateTunAddressIntent(plan); err != nil {
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
		identity, err := e.inspectIdentity(ctx, plan.Interface)
		if err == nil {
			if identity.Kind != "tun" {
				return bound, fmt.Errorf("%w: %s is %s, expected tun", ErrTunLinkIdentityMismatch, plan.Interface, identity.Kind)
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
	identity, err := e.inspectIdentity(ctx, plan.Interface)
	if err != nil {
		return Step{}, fmt.Errorf("inspect TUN link before address apply: %w", err)
	}
	if err := verifyBoundIdentity(plan, identity, false); err != nil {
		return Step{}, err
	}
	addresses, err := e.inspectAddresses(ctx, plan.Interface)
	if err != nil {
		return Step{}, fmt.Errorf("inspect TUN addresses before apply: %w", err)
	}
	exact, conflicts := addressInventoryState(addresses, plan)
	if conflicts > 0 || (exact > 0 && !plan.AllowOwnedExisting) {
		return Step{}, fmt.Errorf("%w: %s already has IPv4 address state that is not authorized by this transaction", ErrTunAddressConflict, plan.Interface)
	}
	if exact > 1 {
		return Step{}, fmt.Errorf("%w: exact address %s appears %d times", ErrTunAddressConflict, plan.CIDR, exact)
	}
	step = tunAddressStep(plan)
	if exact == 0 {
		if err := runCommand(ctx, e.Runner, "ip", "-4", "address", "replace", plan.CIDR, "dev", plan.Interface); err != nil {
			return Step{}, fmt.Errorf("assign TUN address %s to %s: %w", plan.CIDR, plan.Interface, err)
		}
	}
	if err := runCommand(ctx, e.Runner, "ip", "link", "set", "dev", plan.Interface, "up"); err != nil {
		return step, fmt.Errorf("bring addressed TUN link %s up: %w", plan.Interface, err)
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
	identity, err := e.inspectIdentity(ctx, plan.Interface)
	if err != nil {
		return fmt.Errorf("inspect TUN link during address verification: %w", err)
	}
	if err := verifyBoundIdentity(plan, identity, true); err != nil {
		return err
	}
	addresses, err := e.inspectAddresses(ctx, plan.Interface)
	if err != nil {
		return fmt.Errorf("inspect TUN addresses during verification: %w", err)
	}
	exact := 0
	for _, address := range addresses {
		if address.CIDR != plan.CIDR {
			continue
		}
		if address.Interface != plan.Interface || address.Family != "ipv4" {
			return fmt.Errorf("TUN address %s is attached to unexpected identity", plan.CIDR)
		}
		if plan.Scope != "" && address.Scope != plan.Scope {
			return fmt.Errorf("TUN address %s has scope %s, expected %s", plan.CIDR, address.Scope, plan.Scope)
		}
		exact++
	}
	if exact != 1 {
		return fmt.Errorf("TUN address %s must exist exactly once on %s; found %d", plan.CIDR, plan.Interface, exact)
	}
	return nil
}

func (e IPTunAddressExecutor) Rollback(ctx context.Context, plan planner.TunAddressPlan) error {
	if strings.TrimSpace(plan.CIDR) == "" || strings.TrimSpace(plan.Interface) == "" {
		return nil
	}
	if err := validateBoundTunAddressPlan(plan); err != nil {
		return err
	}
	identity, err := e.inspectIdentity(ctx, plan.Interface)
	if err != nil {
		if plan.AllowMissingLink && resourceMissing(err) {
			return nil
		}
		return fmt.Errorf("inspect TUN link before address rollback: %w", err)
	}
	if err := verifyBoundIdentity(plan, identity, false); err != nil {
		return err
	}
	addresses, err := e.inspectAddresses(ctx, plan.Interface)
	if err != nil {
		return fmt.Errorf("inspect TUN addresses before rollback: %w", err)
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
	if err := runCommand(ctx, e.Runner, "ip", "-4", "address", "del", plan.CIDR, "dev", plan.Interface); err != nil && !resourceMissing(err) {
		return fmt.Errorf("remove exact TUN address %s from %s: %w", plan.CIDR, plan.Interface, err)
	}
	return nil
}

type tunLinkIdentity struct {
	Index int
	Kind  string
	Up    bool
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
	if plan.Action != planner.TunAddressActionAssign {
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
	return Step{
		Kind:        "tun-address",
		Target:      fmt.Sprintf("%s@ifindex=%d:%s", plan.Interface, plan.LinkIndex, plan.CIDR),
		Description: plan.Reason,
		Owner:       OwnerTunAddress,
	}
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
